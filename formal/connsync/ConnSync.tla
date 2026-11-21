----------------------------- MODULE ConnSync -----------------------------
(***************************************************************************)
(* Stream synchronisation on one pooled connection.                       *)
(*                                                                         *)
(* Can a caller receive a reply the server produced for another request,  *)
(* and can a connection go back to the pool with a reply still owed?      *)
(*                                                                         *)
(* Models, as one client process running operations one after another on *)
(* the same connection (the pool hands a connection to one operation at a *)
(* time):                                                                  *)
(*   Connection.Execute and ServerPool.execRequestDirect (single request); *)
(*   Connection.ExecuteBatch and ServerPool.execBatchDirect (pipeline);    *)
(*   Commands.executeBatch (rejects a batch with any error reply);         *)
(*   checkExecutable (no quiet request, no mn in a batch);               *)
(*   meta.ValidateRequest, meta.ValidateResponse, meta.ShouldCloseConnection; *)
(*   ServerPool.acquireHealthy's checkAlive probe (Buffered or readable).  *)
(* The bufio.Writer is modelled (wbuf): requests written but not flushed  *)
(* go out with the next flush. The bufio.Reader is not: s2c is everything *)
(* the client can read.                                                    *)
(*                                                                         *)
(* The server process follows memcached 1.6.39 (proto_text.c, memcached.c) *)
(* as catalogued in the "Server behaviours" table of README.md. 1.6.39 is  *)
(* the worst case: from 1.6.40 a too_many ms has its data swallowed.       *)
(*   - one reply per request, the nominal one suppressed by the q flag     *)
(*     (mg: EN, ms: HD, md: HD); errors are never suppressed;              *)
(*   - SERVER_ERROR as the single reply (ms swallows its data block);      *)
(*   - SERVER_ERROR then close, dropping unread input (out of memory       *)
(*     reading a request);                                                 *)
(*   - an ms with 17+ flag tokens: CLIENT_ERROR, then the data block is    *)
(*     run as command lines, producing replies to no request;              *)
(*   - a command line over 2048 bytes: close without a reply;              *)
(*   - the peer going away at any time, possibly mid-reply (truncated);    *)
(*   - any delay: the client's deadline can fire whenever it waits.        *)
(* Not modelled: ma, flush_all, stats (ExecuteStats destroys the          *)
(* connection on any error), extstore, TLS.                                *)
(*                                                                         *)
(* Mutant names a seeded bug; ConnSync.mutant-*.cfg must catch each one:   *)
(*   "parse_error_reusable"  a *ParseError keeps the connection           *)
(*   "client_error_reusable" a *ClientError keeps the connection          *)
(*   "release_on_timeout"    a socket timeout keeps the connection         *)
(*   "no_batch_bound"        ExecuteBatch appends past len(reqs)           *)
(*   "lazy_batch_validation" ExecuteBatch validates each request as it     *)
(*                           writes it, not all of them first              *)
(*   "accept_unframeable"    quiet requests and a caller's mn are sent, as *)
(*                           before the F1 fix                             *)
(***************************************************************************)
EXTENDS Naturals, Sequences, FiniteSets

CONSTANTS
    MaxOps,       \* operations run on the connection
    MaxBatch,     \* requests per batch
    MaxInjected,  \* replies to no request after a too_many ms
    Mutant

Mutants == {"none", "parse_error_reusable", "client_error_reusable",
            "release_on_timeout", "no_batch_bound", "lazy_batch_validation",
            "accept_unframeable"}
ASSUME Mutant \in Mutants

(***************************************************************************)
(* Requests. flaw is what is unusual about one:                           *)
(*   "none"     well formed                                                *)
(*   "invalid"  rejected by meta.ValidateRequest, nothing written          *)
(*   "too_many" an ms with 17+ flag tokens (nothing in the client stops it) *)
(*   "long"     a command line over 2048 bytes (nothing stops it either)   *)
(* mg always carries v, as Get and MultiGet send it.                       *)
(***************************************************************************)
Shape(c, q, f) == [cmd |-> c, quiet |-> q, flaw |-> f]

\* Commands.Get/Set/Delete, ServerPool.Execute, and Ping (mn, which skips
\* checkExecutable). A caller can build a quiet request; it is rejected.
SingleShapes ==
    { Shape("mg", FALSE, "none"), Shape("ms", FALSE, "none"),
      Shape("md", FALSE, "none"), Shape("mn", FALSE, "none"),
      Shape("mg", TRUE, "none"),
      Shape("ms", FALSE, "too_many"), Shape("mg", FALSE, "long"),
      Shape("mg", FALSE, "invalid") }

\* What Commands.MultiGet/MultiSet/MultiDelete build.
CommandsBatchShapes ==
    { Shape("mg", FALSE, "none"), Shape("ms", FALSE, "none"),
      Shape("md", FALSE, "none") }

\* What a caller can pass to ServerPool.ExecuteBatch: anything, quiet requests
\* and mn included. checkExecutable rejects those two before writing.
PoolBatchShapes ==
    SingleShapes \cup { Shape("ms", TRUE, "none"), Shape("md", TRUE, "none") }

Req(s, id) == [cmd |-> s.cmd, quiet |-> s.quiet, flaw |-> s.flaw, id |-> id]

\* ExecuteBatch's end-of-batch marker. Request ids start at 1.
Marker == Req(Shape("mn", FALSE, "none"), 0)

SeqsUpTo(S, lo, hi) == UNION {[1..k -> S] : k \in lo..hi}

(***************************************************************************)
(* Replies: for is the id of the request that produced it (0: none).      *)
(***************************************************************************)
Errors == {"CLIENT_ERROR", "SERVER_ERROR", "ERROR"}

\* Statuses the server produces for a well-formed request.
Produces(cmd) ==
    CASE cmd = "mg" -> {"VA", "EN"}
      [] cmd = "ms" -> {"HD", "NS"}
      [] cmd = "md" -> {"HD", "NF"}
      [] cmd = "mn" -> {"MN"}

\* Nominal statuses the q flag suppresses.
Suppressed(cmd) ==
    CASE cmd = "mg" -> {"EN"}
      [] cmd = "ms" -> {"HD"}
      [] cmd = "md" -> {"HD"}
      [] cmd = "mn" -> {}

\* meta.statusAllowed (mg with v).
Allowed(cmd) ==
    CASE cmd = "mg" -> {"VA", "EN"}
      [] cmd = "ms" -> {"HD", "NS", "EX", "NF"}
      [] cmd = "md" -> {"HD", "NF", "EX", "NS"}
      [] cmd = "mn" -> {"MN"}

\* What running a data block as command lines can answer.
InjectedStatuses == {"VA", "HD", "MN", "ERROR"}

(***************************************************************************)
(* Client errors and the reuse decision.                                  *)
(***************************************************************************)
ErrKind(st) ==
    CASE st = "CLIENT_ERROR" -> "client"
      [] st = "SERVER_ERROR" -> "server"
      [] st = "ERROR"        -> "generic"

\* meta.ShouldCloseConnection, and its conservative true for untyped errors
\* (io.EOF, net timeouts).
ShouldClose(e) ==
    CASE e = "none"    -> FALSE
      [] e = "invalid" -> FALSE
      [] e = "server"  -> FALSE
      [] e = "client"  -> Mutant # "client_error_reusable"
      [] e = "generic" -> TRUE
      [] e = "parse"   -> Mutant # "parse_error_reusable"
      [] e = "timeout" -> Mutant # "release_on_timeout"
      [] e = "eof"     -> TRUE

HasQuiet(rs) == \E k \in DOMAIN rs : rs[k].quiet

\* checkExecutable: requests that cannot be matched to exactly one reply.
Unframeable(r) == r.quiet \/ r.cmd = "mn"
Checked == Mutant # "accept_unframeable"

(* --algorithm ConnSync

variables
    wbuf = <<>>,         \* written, not flushed (bufio.Writer)
    c2s = <<>>,          \* flushed, not yet read by the server
    s2c = <<>>,          \* sent by the server, not yet read by the client
    peerClosed = FALSE,  \* the server side is gone
    nextId = 1,
    ops = 0,
    \* Non-error replies handed to a caller, with the request they answered.
    delivered = {};

define
    \* The connection owes nothing: safe to hand to the next operation.
    Clean == wbuf = <<>> /\ c2s = <<>> /\ s2c = <<>>
end define;

macro destroy() begin
    wbuf := <<>>;
    c2s := <<>>;
    s2c := <<>>;
    peerClosed := FALSE;
end macro;

process client = "client"
variables
    layer = "none",
    reqs = <<>>,
    resps = <<>>,
    i = 1,
    err = "none";
begin
Loop:
    while ops < MaxOps do
    Acquire:
        \* acquireHealthy probes a connection idle for IdleConnCheckAfter;
        \* one that cycled recently is trusted without a probe.
        either
            skip;
        or
            if s2c # <<>> \/ peerClosed then
                destroy();
            end if;
        end either;
    Choose:
        ops := ops + 1;
        err := "none";
        resps := <<>>;
        either
            with s \in SingleShapes do
                reqs := <<Req(s, nextId)>>;
            end with;
            layer := "single";
        or
            with b \in SeqsUpTo(CommandsBatchShapes, 1, MaxBatch) do
                reqs := [k \in DOMAIN b |-> Req(b[k], nextId + k - 1)];
            end with;
            layer := "commands_batch";
        or
            with b \in SeqsUpTo(PoolBatchShapes, 1, MaxBatch) do
                reqs := [k \in DOMAIN b |-> Req(b[k], nextId + k - 1)];
            end with;
            layer := "pool_batch";
        end either;
    Assign:
        nextId := nextId + Len(reqs);
        if layer = "single" then
            goto SValidate;
        else
            goto BValidate;
        end if;

    \* ---- Connection.Execute ------------------------------------------
    SValidate:
        \* checkExecutable and meta.WriteRequest reject before writing
        \* anything (the single mn is Ping, which skips checkExecutable).
        if reqs[1].flaw = "invalid" \/ (Checked /\ reqs[1].quiet) then
            err := "invalid";
            goto Finish;
        end if;
    SWrite:
        wbuf := Append(wbuf, reqs[1]);
    SFlush:
        c2s := c2s \o wbuf;
        wbuf := <<>>;
    SRead:
        either
            await s2c # <<>>;
            with r = Head(s2c) do
                s2c := Tail(s2c);
                if r.st = "TRUNC" then
                    err := "parse";
                elsif r.st \in Errors then
                    \* resp.Error: fn runs, connErr is the protocol error.
                    err := ErrKind(r.st);
                elsif r.st \notin Allowed(reqs[1].cmd) then
                    err := "parse"; \* meta.ValidateResponse
                else
                    delivered := delivered \cup {[req |-> reqs[1].id, rep |-> r]};
                end if;
            end with;
        or
            await s2c = <<>> /\ peerClosed;
            err := "eof";
        or
            await s2c = <<>>;
            err := "timeout";
        end either;
        goto Finish;

    \* ---- Connection.ExecuteBatch -------------------------------------
    BValidate:
        if Checked /\ \E k \in DOMAIN reqs : Unframeable(reqs[k]) then
            err := "invalid";
            goto Finish;
        elsif Mutant # "lazy_batch_validation" /\ \E k \in DOMAIN reqs : reqs[k].flaw = "invalid" then
            err := "invalid";
            goto Finish;
        else
            i := 1;
        end if;
    BWrite:
        while i <= Len(reqs) do
            if reqs[i].flaw = "invalid" then
                \* Only reachable with lazy_batch_validation.
                err := "invalid";
                goto Finish;
            else
                wbuf := Append(wbuf, reqs[i]);
                i := i + 1;
            end if;
        end while;
    BMarker:
        wbuf := Append(wbuf, Marker);
    BFlush:
        c2s := c2s \o wbuf;
        wbuf := <<>>;
    BRead:
        either
            await s2c # <<>>;
            with r = Head(s2c) do
                s2c := Tail(s2c);
                if r.st = "TRUNC" then
                    err := "parse";
                    goto Finish;
                elsif r.st = "MN" then
                    goto BCheck;
                elsif Len(resps) = Len(reqs) /\ Mutant # "no_batch_bound" then
                    err := "parse";
                    goto Finish;
                else
                    resps := Append(resps, r);
                    goto BRead;
                end if;
            end with;
        or
            await s2c = <<>> /\ peerClosed;
            err := "eof";
            goto Finish;
        or
            await s2c = <<>>;
            err := "timeout";
            goto Finish;
        end either;
    BCheck:
        if ~HasQuiet(reqs) then
            if Len(resps) # Len(reqs) then
                err := "parse";
                goto Finish;
            elsif \E k \in DOMAIN resps :
                    resps[k].st \notin Errors /\ resps[k].st \notin Allowed(reqs[k].cmd) then
                err := "parse";
                goto Finish;
            end if;
        end if;
    BConsume:
        \* execBatchDirect: destroy when a reply carries a closing error.
        with closing = {ErrKind(resps[k].st) : k \in {k \in DOMAIN resps : resps[k].st \in Errors}} do
            if \E e \in closing : ShouldClose(e) then
                err := CHOOSE e \in closing : ShouldClose(e);
            end if;
        end with;
        \* Positional results, as promised without quiet requests. Commands
        \* also rejects the whole batch when any reply is an error.
        if ~HasQuiet(reqs)
           /\ (layer = "pool_batch" \/ \A k \in DOMAIN resps : resps[k].st \notin Errors) then
            delivered := delivered \cup
                {[req |-> reqs[k].id, rep |-> resps[k]] :
                    k \in {k \in DOMAIN resps : resps[k].st \notin Errors}};
        end if;

    \* ---- execRequestDirect / execBatchDirect: release or destroy ------
    Finish:
        if ShouldClose(err) then
            destroy();
            goto Loop;
        end if;
    Release:
        skip;
    end while;
end process;

process server = "server"
begin
Serve:
    while TRUE do
        either
            await c2s # <<>> /\ ~peerClosed;
            with r = Head(c2s) do
                if r.flaw = "long" then
                    \* No newline within 2048 bytes: memcached closes.
                    c2s := <<>>;
                    peerClosed := TRUE;
                elsif r.flaw = "too_many" then
                    \* CLIENT_ERROR without swallowing the data block, which
                    \* memcached then reads as command lines.
                    c2s := Tail(c2s);
                    with inj \in SeqsUpTo(InjectedStatuses, 0, MaxInjected) do
                        s2c := s2c \o <<[for |-> r.id, st |-> "CLIENT_ERROR"]>>
                                   \o [k \in DOMAIN inj |-> [for |-> 0, st |-> inj[k]]];
                    end with;
                else
                    either
                        c2s := Tail(c2s);
                        with st \in Produces(r.cmd) do
                            if ~(r.quiet /\ st \in Suppressed(r.cmd)) then
                                s2c := Append(s2c, [for |-> r.id, st |-> st]);
                            end if;
                        end with;
                    or
                        await r.cmd # "mn";
                        c2s := Tail(c2s);
                        s2c := Append(s2c, [for |-> r.id, st |-> "SERVER_ERROR"]);
                    or
                        \* Out of memory reading the request: reply, close,
                        \* drop the unread input.
                        await r.cmd # "mn";
                        c2s := <<>>;
                        s2c := Append(s2c, [for |-> r.id, st |-> "SERVER_ERROR"]);
                        peerClosed := TRUE;
                    end either;
                end if;
            end with;
        or
            \* The peer goes away (crash, reset, idle timeout). When it was
            \* answering a request, the reply can be cut short.
            await ~peerClosed;
            either
                skip;
            or
                await c2s # <<>>;
                s2c := Append(s2c, [for |-> Head(c2s).id, st |-> "TRUNC"]);
            end either;
            c2s := <<>>;
            peerClosed := TRUE;
        end either;
    end while;
end process;

end algorithm; *)
\* BEGIN TRANSLATION
VARIABLES wbuf, c2s, s2c, peerClosed, nextId, ops, delivered, pc

(* define statement *)
Clean == wbuf = <<>> /\ c2s = <<>> /\ s2c = <<>>

VARIABLES layer, reqs, resps, i, err

vars == << wbuf, c2s, s2c, peerClosed, nextId, ops, delivered, pc, layer, 
           reqs, resps, i, err >>

ProcSet == {"client"} \cup {"server"}

Init == (* Global variables *)
        /\ wbuf = <<>>
        /\ c2s = <<>>
        /\ s2c = <<>>
        /\ peerClosed = FALSE
        /\ nextId = 1
        /\ ops = 0
        /\ delivered = {}
        (* Process client *)
        /\ layer = "none"
        /\ reqs = <<>>
        /\ resps = <<>>
        /\ i = 1
        /\ err = "none"
        /\ pc = [self \in ProcSet |-> CASE self = "client" -> "Loop"
                                        [] self = "server" -> "Serve"]

Loop == /\ pc["client"] = "Loop"
        /\ IF ops < MaxOps
              THEN /\ pc' = [pc EXCEPT !["client"] = "Acquire"]
              ELSE /\ pc' = [pc EXCEPT !["client"] = "Done"]
        /\ UNCHANGED << wbuf, c2s, s2c, peerClosed, nextId, ops, delivered, 
                        layer, reqs, resps, i, err >>

Acquire == /\ pc["client"] = "Acquire"
           /\ \/ /\ TRUE
                 /\ UNCHANGED <<wbuf, c2s, s2c, peerClosed>>
              \/ /\ IF s2c # <<>> \/ peerClosed
                       THEN /\ wbuf' = <<>>
                            /\ c2s' = <<>>
                            /\ s2c' = <<>>
                            /\ peerClosed' = FALSE
                       ELSE /\ TRUE
                            /\ UNCHANGED << wbuf, c2s, s2c, peerClosed >>
           /\ pc' = [pc EXCEPT !["client"] = "Choose"]
           /\ UNCHANGED << nextId, ops, delivered, layer, reqs, resps, i, err >>

Choose == /\ pc["client"] = "Choose"
          /\ ops' = ops + 1
          /\ err' = "none"
          /\ resps' = <<>>
          /\ \/ /\ \E s \in SingleShapes:
                     reqs' = <<Req(s, nextId)>>
                /\ layer' = "single"
             \/ /\ \E b \in SeqsUpTo(CommandsBatchShapes, 1, MaxBatch):
                     reqs' = [k \in DOMAIN b |-> Req(b[k], nextId + k - 1)]
                /\ layer' = "commands_batch"
             \/ /\ \E b \in SeqsUpTo(PoolBatchShapes, 1, MaxBatch):
                     reqs' = [k \in DOMAIN b |-> Req(b[k], nextId + k - 1)]
                /\ layer' = "pool_batch"
          /\ pc' = [pc EXCEPT !["client"] = "Assign"]
          /\ UNCHANGED << wbuf, c2s, s2c, peerClosed, nextId, delivered, i >>

Assign == /\ pc["client"] = "Assign"
          /\ nextId' = nextId + Len(reqs)
          /\ IF layer = "single"
                THEN /\ pc' = [pc EXCEPT !["client"] = "SValidate"]
                ELSE /\ pc' = [pc EXCEPT !["client"] = "BValidate"]
          /\ UNCHANGED << wbuf, c2s, s2c, peerClosed, ops, delivered, layer, 
                          reqs, resps, i, err >>

SValidate == /\ pc["client"] = "SValidate"
             /\ IF reqs[1].flaw = "invalid" \/ (Checked /\ reqs[1].quiet)
                   THEN /\ err' = "invalid"
                        /\ pc' = [pc EXCEPT !["client"] = "Finish"]
                   ELSE /\ pc' = [pc EXCEPT !["client"] = "SWrite"]
                        /\ err' = err
             /\ UNCHANGED << wbuf, c2s, s2c, peerClosed, nextId, ops, 
                             delivered, layer, reqs, resps, i >>

SWrite == /\ pc["client"] = "SWrite"
          /\ wbuf' = Append(wbuf, reqs[1])
          /\ pc' = [pc EXCEPT !["client"] = "SFlush"]
          /\ UNCHANGED << c2s, s2c, peerClosed, nextId, ops, delivered, layer, 
                          reqs, resps, i, err >>

SFlush == /\ pc["client"] = "SFlush"
          /\ c2s' = c2s \o wbuf
          /\ wbuf' = <<>>
          /\ pc' = [pc EXCEPT !["client"] = "SRead"]
          /\ UNCHANGED << s2c, peerClosed, nextId, ops, delivered, layer, reqs, 
                          resps, i, err >>

SRead == /\ pc["client"] = "SRead"
         /\ \/ /\ s2c # <<>>
               /\ LET r == Head(s2c) IN
                    /\ s2c' = Tail(s2c)
                    /\ IF r.st = "TRUNC"
                          THEN /\ err' = "parse"
                               /\ UNCHANGED delivered
                          ELSE /\ IF r.st \in Errors
                                     THEN /\ err' = ErrKind(r.st)
                                          /\ UNCHANGED delivered
                                     ELSE /\ IF r.st \notin Allowed(reqs[1].cmd)
                                                THEN /\ err' = "parse"
                                                     /\ UNCHANGED delivered
                                                ELSE /\ delivered' = (delivered \cup {[req |-> reqs[1].id, rep |-> r]})
                                                     /\ err' = err
            \/ /\ s2c = <<>> /\ peerClosed
               /\ err' = "eof"
               /\ UNCHANGED <<s2c, delivered>>
            \/ /\ s2c = <<>>
               /\ err' = "timeout"
               /\ UNCHANGED <<s2c, delivered>>
         /\ pc' = [pc EXCEPT !["client"] = "Finish"]
         /\ UNCHANGED << wbuf, c2s, peerClosed, nextId, ops, layer, reqs, 
                         resps, i >>

BValidate == /\ pc["client"] = "BValidate"
             /\ IF Checked /\ \E k \in DOMAIN reqs : Unframeable(reqs[k])
                   THEN /\ err' = "invalid"
                        /\ pc' = [pc EXCEPT !["client"] = "Finish"]
                        /\ i' = i
                   ELSE /\ IF Mutant # "lazy_batch_validation" /\ \E k \in DOMAIN reqs : reqs[k].flaw = "invalid"
                              THEN /\ err' = "invalid"
                                   /\ pc' = [pc EXCEPT !["client"] = "Finish"]
                                   /\ i' = i
                              ELSE /\ i' = 1
                                   /\ pc' = [pc EXCEPT !["client"] = "BWrite"]
                                   /\ err' = err
             /\ UNCHANGED << wbuf, c2s, s2c, peerClosed, nextId, ops, 
                             delivered, layer, reqs, resps >>

BWrite == /\ pc["client"] = "BWrite"
          /\ IF i <= Len(reqs)
                THEN /\ IF reqs[i].flaw = "invalid"
                           THEN /\ err' = "invalid"
                                /\ pc' = [pc EXCEPT !["client"] = "Finish"]
                                /\ UNCHANGED << wbuf, i >>
                           ELSE /\ wbuf' = Append(wbuf, reqs[i])
                                /\ i' = i + 1
                                /\ pc' = [pc EXCEPT !["client"] = "BWrite"]
                                /\ err' = err
                ELSE /\ pc' = [pc EXCEPT !["client"] = "BMarker"]
                     /\ UNCHANGED << wbuf, i, err >>
          /\ UNCHANGED << c2s, s2c, peerClosed, nextId, ops, delivered, layer, 
                          reqs, resps >>

BMarker == /\ pc["client"] = "BMarker"
           /\ wbuf' = Append(wbuf, Marker)
           /\ pc' = [pc EXCEPT !["client"] = "BFlush"]
           /\ UNCHANGED << c2s, s2c, peerClosed, nextId, ops, delivered, layer, 
                           reqs, resps, i, err >>

BFlush == /\ pc["client"] = "BFlush"
          /\ c2s' = c2s \o wbuf
          /\ wbuf' = <<>>
          /\ pc' = [pc EXCEPT !["client"] = "BRead"]
          /\ UNCHANGED << s2c, peerClosed, nextId, ops, delivered, layer, reqs, 
                          resps, i, err >>

BRead == /\ pc["client"] = "BRead"
         /\ \/ /\ s2c # <<>>
               /\ LET r == Head(s2c) IN
                    /\ s2c' = Tail(s2c)
                    /\ IF r.st = "TRUNC"
                          THEN /\ err' = "parse"
                               /\ pc' = [pc EXCEPT !["client"] = "Finish"]
                               /\ resps' = resps
                          ELSE /\ IF r.st = "MN"
                                     THEN /\ pc' = [pc EXCEPT !["client"] = "BCheck"]
                                          /\ UNCHANGED << resps, err >>
                                     ELSE /\ IF Len(resps) = Len(reqs) /\ Mutant # "no_batch_bound"
                                                THEN /\ err' = "parse"
                                                     /\ pc' = [pc EXCEPT !["client"] = "Finish"]
                                                     /\ resps' = resps
                                                ELSE /\ resps' = Append(resps, r)
                                                     /\ pc' = [pc EXCEPT !["client"] = "BRead"]
                                                     /\ err' = err
            \/ /\ s2c = <<>> /\ peerClosed
               /\ err' = "eof"
               /\ pc' = [pc EXCEPT !["client"] = "Finish"]
               /\ UNCHANGED <<s2c, resps>>
            \/ /\ s2c = <<>>
               /\ err' = "timeout"
               /\ pc' = [pc EXCEPT !["client"] = "Finish"]
               /\ UNCHANGED <<s2c, resps>>
         /\ UNCHANGED << wbuf, c2s, peerClosed, nextId, ops, delivered, layer, 
                         reqs, i >>

BCheck == /\ pc["client"] = "BCheck"
          /\ IF ~HasQuiet(reqs)
                THEN /\ IF Len(resps) # Len(reqs)
                           THEN /\ err' = "parse"
                                /\ pc' = [pc EXCEPT !["client"] = "Finish"]
                           ELSE /\ IF \E k \in DOMAIN resps :
                                        resps[k].st \notin Errors /\ resps[k].st \notin Allowed(reqs[k].cmd)
                                      THEN /\ err' = "parse"
                                           /\ pc' = [pc EXCEPT !["client"] = "Finish"]
                                      ELSE /\ pc' = [pc EXCEPT !["client"] = "BConsume"]
                                           /\ err' = err
                ELSE /\ pc' = [pc EXCEPT !["client"] = "BConsume"]
                     /\ err' = err
          /\ UNCHANGED << wbuf, c2s, s2c, peerClosed, nextId, ops, delivered, 
                          layer, reqs, resps, i >>

BConsume == /\ pc["client"] = "BConsume"
            /\ LET closing == {ErrKind(resps[k].st) : k \in {k \in DOMAIN resps : resps[k].st \in Errors}} IN
                 IF \E e \in closing : ShouldClose(e)
                    THEN /\ err' = (CHOOSE e \in closing : ShouldClose(e))
                    ELSE /\ TRUE
                         /\ err' = err
            /\ IF ~HasQuiet(reqs)
                  /\ (layer = "pool_batch" \/ \A k \in DOMAIN resps : resps[k].st \notin Errors)
                  THEN /\ delivered' = (         delivered \cup
                                        {[req |-> reqs[k].id, rep |-> resps[k]] :
                                            k \in {k \in DOMAIN resps : resps[k].st \notin Errors}})
                  ELSE /\ TRUE
                       /\ UNCHANGED delivered
            /\ pc' = [pc EXCEPT !["client"] = "Finish"]
            /\ UNCHANGED << wbuf, c2s, s2c, peerClosed, nextId, ops, layer, 
                            reqs, resps, i >>

Finish == /\ pc["client"] = "Finish"
          /\ IF ShouldClose(err)
                THEN /\ wbuf' = <<>>
                     /\ c2s' = <<>>
                     /\ s2c' = <<>>
                     /\ peerClosed' = FALSE
                     /\ pc' = [pc EXCEPT !["client"] = "Loop"]
                ELSE /\ pc' = [pc EXCEPT !["client"] = "Release"]
                     /\ UNCHANGED << wbuf, c2s, s2c, peerClosed >>
          /\ UNCHANGED << nextId, ops, delivered, layer, reqs, resps, i, err >>

Release == /\ pc["client"] = "Release"
           /\ TRUE
           /\ pc' = [pc EXCEPT !["client"] = "Loop"]
           /\ UNCHANGED << wbuf, c2s, s2c, peerClosed, nextId, ops, delivered, 
                           layer, reqs, resps, i, err >>

client == Loop \/ Acquire \/ Choose \/ Assign \/ SValidate \/ SWrite
             \/ SFlush \/ SRead \/ BValidate \/ BWrite \/ BMarker \/ BFlush
             \/ BRead \/ BCheck \/ BConsume \/ Finish \/ Release

Serve == /\ pc["server"] = "Serve"
         /\ \/ /\ c2s # <<>> /\ ~peerClosed
               /\ LET r == Head(c2s) IN
                    IF r.flaw = "long"
                       THEN /\ c2s' = <<>>
                            /\ peerClosed' = TRUE
                            /\ s2c' = s2c
                       ELSE /\ IF r.flaw = "too_many"
                                  THEN /\ c2s' = Tail(c2s)
                                       /\ \E inj \in SeqsUpTo(InjectedStatuses, 0, MaxInjected):
                                            s2c' = s2c \o <<[for |-> r.id, st |-> "CLIENT_ERROR"]>>
                                                       \o [k \in DOMAIN inj |-> [for |-> 0, st |-> inj[k]]]
                                       /\ UNCHANGED peerClosed
                                  ELSE /\ \/ /\ c2s' = Tail(c2s)
                                             /\ \E st \in Produces(r.cmd):
                                                  IF ~(r.quiet /\ st \in Suppressed(r.cmd))
                                                     THEN /\ s2c' = Append(s2c, [for |-> r.id, st |-> st])
                                                     ELSE /\ TRUE
                                                          /\ s2c' = s2c
                                             /\ UNCHANGED peerClosed
                                          \/ /\ r.cmd # "mn"
                                             /\ c2s' = Tail(c2s)
                                             /\ s2c' = Append(s2c, [for |-> r.id, st |-> "SERVER_ERROR"])
                                             /\ UNCHANGED peerClosed
                                          \/ /\ r.cmd # "mn"
                                             /\ c2s' = <<>>
                                             /\ s2c' = Append(s2c, [for |-> r.id, st |-> "SERVER_ERROR"])
                                             /\ peerClosed' = TRUE
            \/ /\ ~peerClosed
               /\ \/ /\ TRUE
                     /\ s2c' = s2c
                  \/ /\ c2s # <<>>
                     /\ s2c' = Append(s2c, [for |-> Head(c2s).id, st |-> "TRUNC"])
               /\ c2s' = <<>>
               /\ peerClosed' = TRUE
         /\ pc' = [pc EXCEPT !["server"] = "Serve"]
         /\ UNCHANGED << wbuf, nextId, ops, delivered, layer, reqs, resps, i, 
                         err >>

server == Serve

Next == client \/ server

Spec == Init /\ [][Next]_vars

\* END TRANSLATION

\* A1: every non-error reply a caller receives was produced for its request.
NoMisattribution == \A d \in delivered : d.rep.for = d.req

\* A2: a connection goes back to the pool owing nothing.
ReleasedIsClean == pc["client"] = "Release" => Clean

\* A3: a batch never holds more replies than it sent requests.
BatchBound == Len(resps) <= Len(reqs)

=============================================================================

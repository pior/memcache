---------------------------- MODULE ServerPools ----------------------------
(***************************************************************************)
(* The per-address pool registry against Client.Close.                     *)
(*                                                                         *)
(* Models serverPools.getOrCreate and serverPools.closeAll                 *)
(* (server_pools.go), and the part of Client.Close (client.go) that closes *)
(* the pools closeAll returns. Pool creation is atomic under the write     *)
(* lock, as it is in the code. Reaping and the maintenance pass are not    *)
(* modelled yet.                                                           *)
(*                                                                         *)
(* Mutant names a seeded bug; the matching ServerPools.mutant-*.cfg must   *)
(* produce a counterexample, which shows the properties can fail:          *)
(*   "exists_before_closed": the fast path tests the map hit before the    *)
(*      closed flag, handing out a closed pool after Close.                *)
(*   "no_recheck": the slow path skips the closed re-check under the write *)
(*      lock, creating a pool after closeAll took its snapshot.            *)
(***************************************************************************)
EXTENDS Naturals, FiniteSets

CONSTANTS
    Ops,    \* operation goroutines calling getOrCreate
    Addrs,  \* server addresses they route to
    Mutant  \* "none", or a seeded bug, see above

ASSUME Mutant \in {"none", "exists_before_closed", "no_recheck"}

NoPool == 0

(* --algorithm ServerPools

variables
    \* sync.RWMutex of serverPools.
    readers = 0,
    writer = FALSE,
    \* serverPools fields. A pool is identified by its creation number.
    closed = FALSE,
    byAddr = [a \in Addrs |-> NoPool],
    nextPool = 1,
    \* Pools on which ServerPool.Close has run.
    closedPools = {},
    \* Client.Close has returned.
    clientClosed = FALSE;

define
    Created == 1..(nextPool - 1)

    TypeOK ==
        /\ readers \in 0..Cardinality(Ops)
        /\ writer \in BOOLEAN
        /\ ~(writer /\ readers > 0)
        /\ byAddr \in [Addrs -> {NoPool} \cup Created]

    \* Once Close has returned, no pool is left open: every pool ever created
    \* was in closeAll's snapshot.
    NoOrphanPool == clientClosed => Created \subseteq closedPools
end define;

fair process op \in Ops
variables
    addr \in Addrs,
    startedAfterClose = FALSE,
    sawClosed = FALSE,
    hit = NoPool,
    result = "none";
begin
Start:
    startedAfterClose := clientClosed;
RLock:
    await ~writer;
    readers := readers + 1;
FastRead:
    sawClosed := closed;
    hit := byAddr[addr];
RUnlock:
    readers := readers - 1;
FastPath:
    if Mutant = "exists_before_closed" /\ hit # NoPool then
        result := "pool";
        goto Done;
    elsif sawClosed then
        result := "ErrClientClosed";
        goto Done;
    elsif hit # NoPool then
        result := "pool";
        goto Done;
    end if;
WLock:
    await ~writer /\ readers = 0;
    writer := TRUE;
SlowPath:
    if closed /\ Mutant # "no_recheck" then
        result := "ErrClientClosed";
    elsif byAddr[addr] # NoPool then
        result := "pool";
    else
        byAddr[addr] := nextPool;
        nextPool := nextPool + 1;
        result := "pool";
    end if;
WUnlock:
    writer := FALSE;
end process;

fair process closer = "closer"
variables snapshot = {};
begin
CloseLock:
    await ~writer /\ readers = 0;
    writer := TRUE;
CloseAll:
    closed := TRUE;
    snapshot := {byAddr[a] : a \in Addrs} \ {NoPool};
CloseUnlock:
    writer := FALSE;
ClosePools:
    closedPools := closedPools \cup snapshot;
    clientClosed := TRUE;
end process;

end algorithm; *)
\* BEGIN TRANSLATION
VARIABLES readers, writer, closed, byAddr, nextPool, closedPools, 
          clientClosed, pc

(* define statement *)
Created == 1..(nextPool - 1)

TypeOK ==
    /\ readers \in 0..Cardinality(Ops)
    /\ writer \in BOOLEAN
    /\ ~(writer /\ readers > 0)
    /\ byAddr \in [Addrs -> {NoPool} \cup Created]



NoOrphanPool == clientClosed => Created \subseteq closedPools

VARIABLES addr, startedAfterClose, sawClosed, hit, result, snapshot

vars == << readers, writer, closed, byAddr, nextPool, closedPools, 
           clientClosed, pc, addr, startedAfterClose, sawClosed, hit, result, 
           snapshot >>

ProcSet == (Ops) \cup {"closer"}

Init == (* Global variables *)
        /\ readers = 0
        /\ writer = FALSE
        /\ closed = FALSE
        /\ byAddr = [a \in Addrs |-> NoPool]
        /\ nextPool = 1
        /\ closedPools = {}
        /\ clientClosed = FALSE
        (* Process op *)
        /\ addr \in [Ops -> Addrs]
        /\ startedAfterClose = [self \in Ops |-> FALSE]
        /\ sawClosed = [self \in Ops |-> FALSE]
        /\ hit = [self \in Ops |-> NoPool]
        /\ result = [self \in Ops |-> "none"]
        (* Process closer *)
        /\ snapshot = {}
        /\ pc = [self \in ProcSet |-> CASE self \in Ops -> "Start"
                                        [] self = "closer" -> "CloseLock"]

Start(self) == /\ pc[self] = "Start"
               /\ startedAfterClose' = [startedAfterClose EXCEPT ![self] = clientClosed]
               /\ pc' = [pc EXCEPT ![self] = "RLock"]
               /\ UNCHANGED << readers, writer, closed, byAddr, nextPool, 
                               closedPools, clientClosed, addr, sawClosed, hit, 
                               result, snapshot >>

RLock(self) == /\ pc[self] = "RLock"
               /\ ~writer
               /\ readers' = readers + 1
               /\ pc' = [pc EXCEPT ![self] = "FastRead"]
               /\ UNCHANGED << writer, closed, byAddr, nextPool, closedPools, 
                               clientClosed, addr, startedAfterClose, 
                               sawClosed, hit, result, snapshot >>

FastRead(self) == /\ pc[self] = "FastRead"
                  /\ sawClosed' = [sawClosed EXCEPT ![self] = closed]
                  /\ hit' = [hit EXCEPT ![self] = byAddr[addr[self]]]
                  /\ pc' = [pc EXCEPT ![self] = "RUnlock"]
                  /\ UNCHANGED << readers, writer, closed, byAddr, nextPool, 
                                  closedPools, clientClosed, addr, 
                                  startedAfterClose, result, snapshot >>

RUnlock(self) == /\ pc[self] = "RUnlock"
                 /\ readers' = readers - 1
                 /\ pc' = [pc EXCEPT ![self] = "FastPath"]
                 /\ UNCHANGED << writer, closed, byAddr, nextPool, closedPools, 
                                 clientClosed, addr, startedAfterClose, 
                                 sawClosed, hit, result, snapshot >>

FastPath(self) == /\ pc[self] = "FastPath"
                  /\ IF Mutant = "exists_before_closed" /\ hit[self] # NoPool
                        THEN /\ result' = [result EXCEPT ![self] = "pool"]
                             /\ pc' = [pc EXCEPT ![self] = "Done"]
                        ELSE /\ IF sawClosed[self]
                                   THEN /\ result' = [result EXCEPT ![self] = "ErrClientClosed"]
                                        /\ pc' = [pc EXCEPT ![self] = "Done"]
                                   ELSE /\ IF hit[self] # NoPool
                                              THEN /\ result' = [result EXCEPT ![self] = "pool"]
                                                   /\ pc' = [pc EXCEPT ![self] = "Done"]
                                              ELSE /\ pc' = [pc EXCEPT ![self] = "WLock"]
                                                   /\ UNCHANGED result
                  /\ UNCHANGED << readers, writer, closed, byAddr, nextPool, 
                                  closedPools, clientClosed, addr, 
                                  startedAfterClose, sawClosed, hit, snapshot >>

WLock(self) == /\ pc[self] = "WLock"
               /\ ~writer /\ readers = 0
               /\ writer' = TRUE
               /\ pc' = [pc EXCEPT ![self] = "SlowPath"]
               /\ UNCHANGED << readers, closed, byAddr, nextPool, closedPools, 
                               clientClosed, addr, startedAfterClose, 
                               sawClosed, hit, result, snapshot >>

SlowPath(self) == /\ pc[self] = "SlowPath"
                  /\ IF closed /\ Mutant # "no_recheck"
                        THEN /\ result' = [result EXCEPT ![self] = "ErrClientClosed"]
                             /\ UNCHANGED << byAddr, nextPool >>
                        ELSE /\ IF byAddr[addr[self]] # NoPool
                                   THEN /\ result' = [result EXCEPT ![self] = "pool"]
                                        /\ UNCHANGED << byAddr, nextPool >>
                                   ELSE /\ byAddr' = [byAddr EXCEPT ![addr[self]] = nextPool]
                                        /\ nextPool' = nextPool + 1
                                        /\ result' = [result EXCEPT ![self] = "pool"]
                  /\ pc' = [pc EXCEPT ![self] = "WUnlock"]
                  /\ UNCHANGED << readers, writer, closed, closedPools, 
                                  clientClosed, addr, startedAfterClose, 
                                  sawClosed, hit, snapshot >>

WUnlock(self) == /\ pc[self] = "WUnlock"
                 /\ writer' = FALSE
                 /\ pc' = [pc EXCEPT ![self] = "Done"]
                 /\ UNCHANGED << readers, closed, byAddr, nextPool, 
                                 closedPools, clientClosed, addr, 
                                 startedAfterClose, sawClosed, hit, result, 
                                 snapshot >>

op(self) == Start(self) \/ RLock(self) \/ FastRead(self) \/ RUnlock(self)
               \/ FastPath(self) \/ WLock(self) \/ SlowPath(self)
               \/ WUnlock(self)

CloseLock == /\ pc["closer"] = "CloseLock"
             /\ ~writer /\ readers = 0
             /\ writer' = TRUE
             /\ pc' = [pc EXCEPT !["closer"] = "CloseAll"]
             /\ UNCHANGED << readers, closed, byAddr, nextPool, closedPools, 
                             clientClosed, addr, startedAfterClose, sawClosed, 
                             hit, result, snapshot >>

CloseAll == /\ pc["closer"] = "CloseAll"
            /\ closed' = TRUE
            /\ snapshot' = {byAddr[a] : a \in Addrs} \ {NoPool}
            /\ pc' = [pc EXCEPT !["closer"] = "CloseUnlock"]
            /\ UNCHANGED << readers, writer, byAddr, nextPool, closedPools, 
                            clientClosed, addr, startedAfterClose, sawClosed, 
                            hit, result >>

CloseUnlock == /\ pc["closer"] = "CloseUnlock"
               /\ writer' = FALSE
               /\ pc' = [pc EXCEPT !["closer"] = "ClosePools"]
               /\ UNCHANGED << readers, closed, byAddr, nextPool, closedPools, 
                               clientClosed, addr, startedAfterClose, 
                               sawClosed, hit, result, snapshot >>

ClosePools == /\ pc["closer"] = "ClosePools"
              /\ closedPools' = (closedPools \cup snapshot)
              /\ clientClosed' = TRUE
              /\ pc' = [pc EXCEPT !["closer"] = "Done"]
              /\ UNCHANGED << readers, writer, closed, byAddr, nextPool, addr, 
                              startedAfterClose, sawClosed, hit, result, 
                              snapshot >>

closer == CloseLock \/ CloseAll \/ CloseUnlock \/ ClosePools

(* Allow infinite stuttering to prevent deadlock on termination. *)
Terminating == /\ \A self \in ProcSet: pc[self] = "Done"
               /\ UNCHANGED vars

Next == closer
           \/ (\E self \in Ops: op(self))
           \/ Terminating

Spec == /\ Init /\ [][Next]_vars
        /\ \A self \in Ops : WF_vars(op(self))
        /\ WF_vars(closer)

Termination == <>(\A self \in ProcSet: pc[self] = "Done")

\* END TRANSLATION

\* An operation that started after Close returned fails with ErrClientClosed.
\* One that overlaps Close may get a pool that is being closed: Acquire then
\* fails with ErrPoolClosed. (Defined here, not in the algorithm's define
\* block, because it reads the processes' local variables.)
ClosedMeansClosed ==
    \A o \in Ops :
        (pc[o] = "Done" /\ startedAfterClose[o]) => result[o] = "ErrClientClosed"

=============================================================================

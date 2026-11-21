# connsync: stream synchronisation on one connection

`ConnSync.tla` asks two questions about one pooled connection: can a caller
receive a reply the server produced for another request, and can a connection
go back to the pool with a reply still owed? Its header names the Go functions
it models.

## Server behaviours

The server process follows these behaviours, read from memcached 1.6.39
(`proto_text.c`, `memcached.c`). "Source" means confirmed by reading the code;
"observed" means seen on the wire.

| # | Behaviour | Status | Modelled |
|---|-----------|--------|----------|
| S1 | One reply line per request, plus a data block for `VA`. | source | yes |
| S2 | `q` suppresses only the nominal status: mg `EN`, ms `HD`, md `HD`, ma success. Never an error: every error path goes through `out_errstring`, which clears `noreply`, and `noreply` is reset before each command. | source | yes (no ma) |
| S3 | Every error is exactly one line (`CLIENT_ERROR …`, `SERVER_ERROR …`, `ERROR`). | source | yes |
| S4 | An ms error found after its tokens are parsed swallows the data block: the stream stays in sync ("object too large", "out of memory storing object"). | source | yes (`SERVER_ERROR`) |
| S5 | Up to 1.6.39, an ms error found earlier (key over 250 bytes, missing or bad size, more than 20 tokens) does **not** swallow the data block: memcached runs the value as command lines, producing replies to no request. The client rejects all of these but the token count. Fixed in 1.6.40, which swallows the data in every case. | source; observed on 1.6.39 and 1.6.40–1.6.45 | yes (`too_many`) |
| S6 | A data block not ending in `\r\n`: `CLIENT_ERROR bad data chunk`, then the following bytes are read as commands. The client writes exact lengths, so it cannot cause this. | source | no |
| S7 | A command line with no `\n` within 2048 bytes: the server closes the connection without a reply. Nothing in the client bounds the line length. | source | yes (`long`) |
| S8 | `SERVER_ERROR out of memory reading request`: reply, drop the unread input, close. The client keeps a connection after `SERVER_ERROR`, so the next operation gets EOF: a spurious failure, not a wrong value. | source | yes |
| S9 | A NUL in a key: `CLIENT_ERROR`, then `ERROR`. The client rejects control bytes in keys. | observed | no |
| S10 | Hang, late replies, close or reset at any time, possibly mid-reply. | network | yes |

The model keeps the 1.6.39 behaviour of S5: the client must stay in sync
against older servers too.

## Properties

- `NoMisattribution`: every non-error reply a caller receives was produced
  for its request. An error delivered against the wrong request is
  acceptable: the caller sees a failure, not a wrong value.
- `ReleasedIsClean`: a connection goes back to the pool with nothing
  unflushed, in flight or unread. A server-side close is allowed: the next
  operation gets EOF and destroys the connection.
- `BatchBound`: a batch never holds more replies than it sent requests.

## What it found

An `mn` request inside a batch made `Connection.ExecuteBatch` stop at that
request's `MN`, taking it for its own end marker. With a quiet request in the
batch, nothing checked the reply count, so the connection went back to the
pool owing replies and the next operation read another key's value. Requests
that cannot be matched to exactly one reply (the quiet flag, `mn`, `stats`)
are now rejected before anything is written; the `accept_unframeable` mutant
keeps the old behaviour and must stay caught.

Each mutant except `no_batch_bound` also breaks `NoMisattribution` when it is
the only property checked: each of those guards alone prevents wrong values.
`no_batch_bound` reaches only `BatchBound` now that the reply count is always
checked.

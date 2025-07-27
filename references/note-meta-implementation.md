Since this issue's near the top I'll add some notes: in the memcached proxy we pipeline all storage commands (either old text or new meta). This works so long as you track where the request came from and you don't try to pipeline certain things (watch, stats, etc).

Example:

```
get foo\r\n
set foo 0 0 2\r\n
hi\r\n
mg foo v\r\n
```

... can all be sent down the same pipe and responses can be read in order. Even mix and matching of text and meta protocols works.

Caveats:

Incompatible with noreply - in the proxy I strip noreply/quiet flags.
If CLIENT_ERROR is seen the connection must be closed
If SERVER_ERROR is seen the request may be retried, but the only way to know if the response matches the request is via managing a request stack.

If you're using pure meta protocol and no text protocol, pipelining gets a bit better:

```
mg foo v O1 q\r\n
mg bar v O2 q\r\n
ms 2 baz O3 q\r\n
hi\r\n
mn\r\n
```

In this example we pipeline three commands with the quiet flag and an opaque flag.

If responses are found: mg hits or ms errors, the O is reflected.

mn caps the pipeline set, and when MN\r\n is seen you know all prior commands have been processed.

Caveats:
There's no meta-specific error (yet...), so CLIENT_ERROR and SERVER_ERROR cannot be matched with their requests.
To handle errors you need to:

- Close if CLIENT_ERROR seen
- Process until MN is seen, ignoring errors
- report a generic error state or retry for any keys not seen

In practice it's not the worst thing and might even get improved this year.

Finally, you can do the same as above without quiet flags and just pipeline and read as fast as you want. The memcached server will opportunistically batch responses into fewer syscalls.
IE: if the server wakes and reads multiple requests off the socket at once, it will send as many responses as it can in the same syscall back to the client.

This means even if you do nothing but shove commands down the pipe as they come in, the client will save on recv syscalls.
## Chanserv [![GoDoc](https://godoc.org/github.com/zenhotels/chanserv?status.svg)](https://godoc.org/github.com/zenhotels/chanserv)

```
$ go get github.com/astranet/chanserv
```

Package chanserv provides a simple message queue framework based upon nested Go-lang channels being served using [AstraNet](https://github.com/zenhotels/astranet).

### Wait, what?..

Well, first things first. Chanserv is not a message queue really, but it provides some good building blocks to create one, that's why I call it a framework. Let's look at the primitives that the package works with:

```go
// Frame represents the payload to send over the channel,
// allowing user to implement any serialisation logic by himself.
type Frame interface {
    // Bytes returns a byte representation of the payload.
    Bytes() []byte
}
```

So for the case of MQ, having a `Message` struct that implements the `Bytes()` method via [Cap'n'Proto](https://capnproto.org) or [Protocol-buffers](https://developers.google.com/protocol-buffers/) serialization is sufficient to get your messages delivered. Strict ordering, delivered-only-once guarantee, automatic flow control and retries are the built-in features thanks to [AstraNet](https://github.com/zenhotels/astranet).

Speaking of, [astranet](https://github.com/zenhotels/astranet) is a Go-lang package for managing highly concurrent independent network streams, so chanserv strives to utilize these features at full. Another primitive defines a source of the frames:

```go
// Source represents an announce of the new frame source.
type Source interface {
    // Header gets the application data associated with this source. The source implementation
    // is not required to return any header bytes.
    Header() []byte
    // Out is a read-only channel of frames, generated by some source.
    // On the server side the channel must be closed after sending all the available frames,
    // on the client side it will be closed by chanserv upon a network/timeout error or success on the remote side.
    Out() <-chan Frame
}
```

So now you might be interested how exactly this framework is based upon nested Go-lang channels and where this nesting happens? Let's check out the final primitive that wraps everything up:

```go
// SourceFunc emits frame sources based on the request data provided.
// On the server side the channel must be closed after sending all the source announcements,
// on the client side it will be closed by chanserv upon a network/timeout error or success on the remote side.
type SourceFunc func(reqBody []byte) <-chan Source
```

So now you see, that `<- chan Source` is roughly equivalent to `<-chan <-chan Message`! So the full picture would be that: the server listens for incoming connections, serving the source func; a client connects and submits its payload, which may include some meta data or be just empty. The client is subscribed now to the _sourcing_ channel and is ready to accept source announcements (`Source`). Each source announcement has a header (with meta data or just empty) and the
output frame chan that client may subscribe to (i.e. `range/select`). So its your move then, decide which frame channels you want to range over or select from and consume the results when they become available. Each channel, incuding the source one, will be automatically closed.

On the network side this looks like _N+1_ opened connections: one for the `master` chan to receive initial requests and send addresses of the descendant listeners for the client to connect, another _N_ connections for each frame chan the client would discover and connect to. This could be easily done with TCP ports and an early prototype did that, however you'll hit the 65K limit very soon. [AstraNet](https://github.com/zenhotels/astranet), on the other hand, is a multiplexer, so within our production environment we can afford to serve thousands of channels a second over a single TCP connection.

### Simple start

#### Phase 1: Server

Init a Multiplexer, any that conforms the expected interface.

```go
mpx := astranet.New().Server()
if err := mpx.ListenAndServe("tcp4", ":5555"); err != nil {
    log.Fatalln("[astranet ERR]", err)
}
```

Start the chanserv's Server using this Multiplexer for the network capabilities:
```go
srv := chanserv.NewServer(mpx, chanserv.ServerOnError(func(err error) {
    log.Println("[server WARN]", err)
}))
```

Register your server, so it can be discovered by its address. If you'd stick with AstraNet, it will provide
automatic service discovery features. Do not forget to provide a source function.

```go
    if err := srv.ListenAndServe("chanserv", srcFn); err != nil {
        log.Fatalln("[server ERR]", err)
    }
```

#### Phase 2: Source

Let's prepare the source function to do something cool:

```go
func srcFn(req []byte) <-chan Source {
    out := make(chan Source, 5)
    for i := 0; i < 5; i++ {
        src := testSource{n: i + 1, data: req} // testSource implements Source
        out <- src.Run(time.Millisecond*time.Duration(100*i) + 100)
    }
    close(out)
    return out
}
```

For this case, the function configures and announces five distinct sources and closes the sourcing channel.

Each source maintains a channel of two simple frames:

```go
func (s *testSource) Run(d time.Duration) *testSource {
    frames := make(chan Frame, 2)
    go func() {
        frames <- frame([]byte("wait for me!")) // first frame immediately
        time.Sleep(d) // sleep for a random delay
        frames <- frame([]byte("ok I'm ready")) // second frame
        close(frames) // done with this channel
    }()
    s.frames = frames
    return s
}
```

In a real case the source function could setup some workers using the provided context, this context could be for example a list of endpoints to connect, credentials to use and such. And the workers themselves would implement the `Source` interface and emit a channel of frames with results of their particular job.

#### Phase 3: Client

Init a Multiplexer and connect to the server. Or join the cluster for discovery.

```go
mpx2 := astranet.New().Client()
if err := mpx2.Join("tcp4", "localhost:5555"); err != nil {
    log.Fatalln("[astranet ERR]", err)
}
```

Init and configure a chanserv client.

```go
cli := chanserv.NewClient(mpx2,
    chanserv.ClientDialTimeout(5*time.Second),
    chanserv.ClientOnError(func(err error) {
        log.Println("[client WARN]", err)
    }),
)
```

Lookup the chanserv service and post the request.

```go
sources, err := cli.LookupAndPost("chanserv", []byte("hello"), nil)
if err != nil {
    log.Fatalln("[client ERR]", err)
}
```

At this point all setup is over, now `sources` which is `<-chan Source` is bound to the `srcFn("hello")` result.

#### Phase 4: Consuming

We are ready now to consume the incoming source announcements and frames:

```go
wg := new(sync.WaitGroup)
wg.Add(5) // expecting 5 sources

for src := range sources {
    log.Println("[HEAD]", string(src.Header()))
    go func(src Source) {
        defer wg.Done()
        for frame := range src.Out() {
            log.Printf("got frame: %s", frame.Bytes())
        }
    }(src)
}

wg.Wait()
```

The user is responsible to handle all sources and all the frames, so if you are omitting a source based
on its header, don't forget to skip all frames from it otherwise it will block the receiving chanserv code,
that would block the sending chanserv code too. All channels would automatically close upon network/timeout error or because of end of stream from the server side.

So why I called this a simple message queue framework? Well, anybody can convert this `<- Source` into a `<-chan <-chan Message` object and pass it further to the ponies who wouldn't bother to handle backlogs and the waitgroup-syncing stuff themselves. Then call it easyMQ and sell.

### Demo

```
$ go test
2016/03/17 21:11:36 chanserv_test.go:23: will connect in 3 sec...
2016/03/17 21:11:39 chanserv_test.go:44: [HEAD] source @0, for request: hello
2016/03/17 21:11:39 chanserv_test.go:44: [HEAD] source @1, for request: hello
2016/03/17 21:11:39 chanserv_test.go:44: [HEAD] source @2, for request: hello
2016/03/17 21:11:39 chanserv_test.go:44: [HEAD] source @3, for request: hello
2016/03/17 21:11:39 chanserv_test.go:44: [HEAD] source @4, for request: hello

2016/03/17 21:11:39 chanserv_test.go:48: [FRAME 0 from @5] wait for me!
2016/03/17 21:11:39 chanserv_test.go:48: [FRAME 0 from @1] wait for me!
2016/03/17 21:11:39 chanserv_test.go:48: [FRAME 0 from @4] wait for me!
2016/03/17 21:11:39 chanserv_test.go:48: [FRAME 0 from @3] wait for me!
2016/03/17 21:11:39 chanserv_test.go:48: [FRAME 0 from @2] wait for me!

2016/03/17 21:11:40 chanserv_test.go:48: [FRAME 1 from @1] ok I'm ready
2016/03/17 21:11:41 chanserv_test.go:48: [FRAME 1 from @2] ok I'm ready
2016/03/17 21:11:42 chanserv_test.go:48: [FRAME 1 from @3] ok I'm ready
2016/03/17 21:11:43 chanserv_test.go:48: [FRAME 1 from @4] ok I'm ready
2016/03/17 21:11:44 chanserv_test.go:48: [FRAME 1 from @5] ok I'm ready
PASS
```

## License

MIT

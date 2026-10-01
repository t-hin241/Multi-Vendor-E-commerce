package smtp_test

import (
	"bufio"
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"shopee/backend/services/notification/internal/sender"
	"shopee/backend/services/notification/internal/sender/smtp"
)

// fakeRelay answers one SMTP session; rcpt is the reply to RCPT, and
// dropAfterData closes the connection instead of answering the end of DATA.
func fakeRelay(t *testing.T, rcpt string, dropAfterData bool) (string, string, chan string) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	messages := make(chan string, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
		rw := bufio.NewReadWriter(bufio.NewReader(conn), bufio.NewWriter(conn))
		send := func(s string) { _, _ = rw.WriteString(s + "\r\n"); _ = rw.Flush() }
		send("220 local-test ESMTP")
		var body strings.Builder
		data := false
		for {
			line, err := rw.ReadString('\n')
			if err != nil {
				return
			}
			line = strings.TrimRight(line, "\r\n")
			if data {
				if line == "." {
					if dropAfterData {
						return
					}
					messages <- body.String()
					send("250 accepted")
					data = false
				} else {
					body.WriteString(line + "\n")
				}
				continue
			}
			switch {
			case strings.HasPrefix(line, "EHLO"):
				send("250 local-test")
			case strings.HasPrefix(line, "MAIL"):
				send("250 OK")
			case strings.HasPrefix(line, "RCPT"):
				send(rcpt)
			case line == "DATA":
				data = true
				send("354 send data")
			case line == "QUIT":
				send("221 bye")
				return
			default:
				send("500 unsupported")
			}
		}
	}()
	host, port, _ := net.SplitHostPort(listener.Addr().String())
	return host, port, messages
}

func send(host, port string) error {
	s := smtp.Sender{Relay: smtp.Relay{Host: host, Port: port, From: "sender@example.invalid", AllowPlaintext: true}}
	return s.Send(context.Background(), sender.Email{ToEmail: "buyer@example.invalid", Subject: "Đơn hàng #1 đã được thanh toán", Body: "Xin chào,\n\nNội dung."})
}

func TestSMTPDeliversUTF8AndClassifiesFailures(t *testing.T) {
	host, port, messages := fakeRelay(t, "250 OK", false)
	if err := send(host, port); err != nil {
		t.Fatal(err)
	}
	msg := <-messages
	if !strings.Contains(msg, "Subject: =?utf-8?q?") || !strings.Contains(msg, "charset=UTF-8") || !strings.Contains(msg, "Nội dung.") {
		t.Fatalf("unexpected message: %s", msg)
	}

	var f *sender.Failure
	host, port, _ = fakeRelay(t, "550 no such mailbox", false)
	if err := send(host, port); !errors.As(err, &f) || !f.Permanent {
		t.Fatalf("a 5xx refusal is permanent, got %v", err)
	}
	host, port, _ = fakeRelay(t, "451 try again later", false)
	if err := send(host, port); !errors.As(err, &f) || f.Permanent || f.Uncertain {
		t.Fatalf("a 4xx refusal is transient, got %+v", f)
	}
	host, port, _ = fakeRelay(t, "250 OK", true)
	if err := send(host, port); !errors.As(err, &f) || !f.Uncertain {
		t.Fatalf("a lost connection after DATA is uncertain, got %v", err)
	}
	if err := send("127.0.0.1", "1"); !errors.As(err, &f) || f.Permanent {
		t.Fatalf("an unreachable relay is transient, got %v", err)
	}
	if strings.Contains(f.Error(), "buyer@") {
		t.Fatal("errors must not carry the address")
	}
}

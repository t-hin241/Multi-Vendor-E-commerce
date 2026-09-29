package smtp_test

import (
	"bufio"
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"shopee/backend/services/notification/internal/sender/smtp"
	"shopee/backend/services/notification/internal/usecase"
)

func TestResetEmailDeliveryAndTLSRequirement(t *testing.T) {
	for _, allowPlain := range []bool{true, false} {
		t.Run(map[bool]string{true: "local-mailbox", false: "requires-TLS"}[allowPlain], func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			messages := make(chan string, 1)
			go func() {
				conn, e := listener.Accept()
				if e != nil {
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
					line, e := rw.ReadString('\n')
					if e != nil {
						return
					}
					line = strings.TrimSpace(line)
					if data {
						if line == "." {
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
					case strings.HasPrefix(line, "MAIL"), strings.HasPrefix(line, "RCPT"):
						send("250 OK")
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
			sender := smtp.ResetSender{Host: host, Port: port, From: "sender@example.invalid", AllowPlaintext: allowPlain}
			err = sender.SendReset(context.Background(), &usecase.ResetMessage{Email: "buyer@example.invalid", URL: "https://store.example.invalid/reset-password#token=synthetic-test-token"})
			if !allowPlain {
				if err == nil {
					t.Fatal("unencrypted SMTP accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			select {
			case body := <-messages:
				if !strings.Contains(body, "#token=synthetic-test-token") {
					t.Fatal("reset link missing")
				}
			case <-time.After(time.Second):
				t.Fatal("no delivery")
			}
		})
	}
}

// Package smtp delivers email through an SMTP relay (the production
// sender). TLS is required unless explicitly allowed for a local mailbox.
// Errors never contain the address, the body or the credentials.
package smtp

import (
	"context"
	"crypto/tls"
	"errors"
	"mime"
	"net"
	"net/mail"
	netsmtp "net/smtp"
	"net/textproto"
	"strings"
	"time"

	"shopee/backend/services/notification/internal/sender"
)

// Relay is the SMTP account settings, read from the config layer.
type Relay struct {
	Host, Port, Username, Password, From string
	AllowPlaintext                       bool
}

// Sender is the notification sender for order and vendor messages.
type Sender struct{ Relay Relay }

func (s Sender) Send(ctx context.Context, email sender.Email) error {
	return s.Relay.deliver(ctx, email.ToEmail, email.Subject, email.Body)
}

// refused maps an SMTP reply: 5xx is permanent, anything else transient.
func refused(err error, reason string) error {
	var reply *textproto.Error
	if errors.As(err, &reply) && reply.Code >= 500 && reply.Code < 600 {
		return sender.Permanent(reason)
	}
	return sender.Transient(reason)
}

func (r Relay) deliver(ctx context.Context, to, subject, body string) error {
	if r.Host == "" {
		return sender.Transient("mail sender is not configured")
	}
	recipient, err := mail.ParseAddress(to)
	if err != nil {
		return sender.Permanent("invalid recipient address")
	}
	from, err := mail.ParseAddress(r.From)
	if err != nil {
		return sender.Transient("invalid sender address")
	}
	if strings.ContainsAny(subject, "\r\n") {
		return sender.Permanent("invalid subject")
	}
	conn, err := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "tcp", net.JoinHostPort(r.Host, r.Port))
	if err != nil {
		return sender.Transient("mail connection failed")
	}
	defer conn.Close()
	deadline := time.Now().Add(15 * time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	if err = conn.SetDeadline(deadline); err != nil {
		return sender.Transient("mail deadline failed")
	}
	client, err := netsmtp.NewClient(conn, r.Host)
	if err != nil {
		return sender.Transient("mail handshake failed")
	}
	defer client.Close()
	if ok, _ := client.Extension("STARTTLS"); ok {
		if err = client.StartTLS(&tls.Config{ServerName: r.Host, MinVersion: tls.VersionTLS12}); err != nil {
			return sender.Transient("mail TLS failed")
		}
	} else if !r.AllowPlaintext {
		return sender.Transient("mail TLS required")
	}
	if r.Username != "" {
		if err = client.Auth(netsmtp.PlainAuth("", r.Username, r.Password, r.Host)); err != nil {
			return sender.Transient("mail authentication failed")
		}
	}
	if err = client.Mail(from.Address); err != nil {
		return refused(err, "mail sender rejected")
	}
	if err = client.Rcpt(recipient.Address); err != nil {
		return refused(err, "mail recipient rejected")
	}
	w, err := client.Data()
	if err != nil {
		return refused(err, "mail data refused")
	}
	message := "From: " + from.Address + "\r\nTo: " + recipient.Address + "\r\nSubject: " + mime.QEncoding.Encode("utf-8", subject) +
		"\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\nContent-Transfer-Encoding: 8bit\r\n\r\n" +
		strings.ReplaceAll(strings.ReplaceAll(body, "\r\n", "\n"), "\n", "\r\n")
	if _, err = w.Write([]byte(message)); err != nil {
		return sender.Uncertain("mail write failed")
	}
	if err = w.Close(); err != nil {
		// The server answered the end of DATA: a refusal is final, a lost
		// connection leaves the outcome unknown.
		var reply *textproto.Error
		if errors.As(err, &reply) {
			return refused(err, "mail delivery refused")
		}
		return sender.Uncertain("mail delivery unconfirmed")
	}
	// DATA acceptance is the receipt; QUIT failure must not trigger duplicate delivery.
	_ = client.Quit()
	return nil
}

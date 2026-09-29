package smtp

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/mail"
	netsmtp "net/smtp"
	"strings"
	"time"

	"shopee/backend/services/notification/internal/usecase"
)

type ResetSender struct {
	Host, Port, Username, Password, From string
	AllowPlaintext                       bool
}

func (s ResetSender) SendReset(ctx context.Context, message *usecase.ResetMessage) error {
	if s.Host == "" {
		return fmt.Errorf("reset mail sender is not configured")
	}
	recipient, err := mail.ParseAddress(message.Email)
	if err != nil {
		return fmt.Errorf("invalid reset recipient")
	}
	from, err := mail.ParseAddress(s.From)
	if err != nil {
		return fmt.Errorf("invalid sender")
	}
	conn, err := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "tcp", net.JoinHostPort(s.Host, s.Port))
	if err != nil {
		return fmt.Errorf("mail connection failed")
	}
	defer conn.Close()
	deadline := time.Now().Add(15 * time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	if err = conn.SetDeadline(deadline); err != nil {
		return fmt.Errorf("mail deadline failed")
	}
	client, err := netsmtp.NewClient(conn, s.Host)
	if err != nil {
		return fmt.Errorf("mail handshake failed")
	}
	defer client.Close()
	if ok, _ := client.Extension("STARTTLS"); ok {
		if err = client.StartTLS(&tls.Config{ServerName: s.Host, MinVersion: tls.VersionTLS12}); err != nil {
			return fmt.Errorf("mail TLS failed")
		}
	} else if !s.AllowPlaintext {
		return fmt.Errorf("mail TLS required")
	}
	if s.Username != "" {
		if err = client.Auth(netsmtp.PlainAuth("", s.Username, s.Password, s.Host)); err != nil {
			return fmt.Errorf("mail authentication failed")
		}
	}
	if err = client.Mail(from.Address); err != nil {
		return fmt.Errorf("mail sender rejected")
	}
	if err = client.Rcpt(recipient.Address); err != nil {
		return fmt.Errorf("mail recipient rejected")
	}
	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("mail data unavailable")
	}
	// Plain text; no message body or recipient is included in errors/logs.
	body := "From: " + from.Address + "\r\nTo: " + recipient.Address + "\r\nSubject: Reset your password\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\nUse this one-time link to reset your password:\r\n" + message.URL + "\r\n\r\nIf you did not request this, ignore this message.\r\n"
	if strings.ContainsAny(message.URL, "\r\n") {
		return fmt.Errorf("invalid reset URL")
	}
	if _, err = w.Write([]byte(body)); err != nil {
		return fmt.Errorf("mail write failed")
	}
	if err = w.Close(); err != nil {
		return fmt.Errorf("mail delivery failed")
	}
	// DATA acceptance is the receipt; QUIT failure must not trigger duplicate delivery.
	_ = client.Quit()
	return nil
}

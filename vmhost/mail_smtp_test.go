package vmhost

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"mime"
	"mime/multipart"
	"net"
	"net/mail"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeSMTP is an SMTP server for one test: STARTTLS or TLS from the first
// byte with a certificate made for the test, AUTH with the mechanisms it
// lists, and a 550 for a recipient whose address starts with "reject". It
// keeps each message it accepts.
type fakeSMTP struct {
	t        *testing.T
	ln       net.Listener
	tls      *tls.Config // offered with STARTTLS; nil offers none
	implicit bool        // the listener is TLS from the first byte
	mechs    []string    // AUTH mechanisms offered; none asks for no AUTH
	user     string
	pass     string

	mu   sync.Mutex
	got  []delivery
	cmds []string
}

type delivery struct {
	from  string
	rcpts []string
	data  string
	tls   bool
	user  string
}

func startFakeSMTP(t *testing.T, s *fakeSMTP) *fakeSMTP {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	if s.implicit {
		ln = tls.NewListener(ln, s.tls)
	}
	s.t, s.ln = t, ln
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go s.serve(c)
		}
	}()
	t.Cleanup(func() { ln.Close() })
	return s
}

func (s *fakeSMTP) port() int { return s.ln.Addr().(*net.TCPAddr).Port }

func (s *fakeSMTP) deliveries() []delivery {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]delivery(nil), s.got...)
}

func (s *fakeSMTP) commands() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.cmds...)
}

func (s *fakeSMTP) serve(c net.Conn) {
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(30 * time.Second))
	encrypted := s.implicit
	r := bufio.NewReader(c)
	say := func(line string) { fmt.Fprintf(c, "%s\r\n", line) }
	read := func() (string, bool) {
		line, err := r.ReadString('\n')
		if err != nil {
			return "", false
		}
		line = strings.TrimSuffix(line, "\n")
		if !strings.HasSuffix(line, "\r") {
			say("500 a line must end in CRLF")
			return "", false
		}
		return strings.TrimSuffix(line, "\r"), true
	}
	var user, from string
	var rcpts []string
	say("220 fake ESMTP")
	for {
		line, ok := read()
		if !ok {
			return
		}
		verb, arg, _ := strings.Cut(line, " ")
		verb = strings.ToUpper(verb)
		s.mu.Lock()
		s.cmds = append(s.cmds, verb)
		s.mu.Unlock()
		switch verb {
		case "EHLO":
			ext := []string{"fake"}
			if s.tls != nil && !encrypted {
				ext = append(ext, "STARTTLS")
			}
			if len(s.mechs) > 0 {
				ext = append(ext, "AUTH "+strings.Join(s.mechs, " "))
			}
			ext = append(ext, "8BITMIME")
			for i, e := range ext {
				sep := "-"
				if i == len(ext)-1 {
					sep = " "
				}
				say("250" + sep + e)
			}
		case "STARTTLS":
			say("220 go ahead")
			tc := tls.Server(c, s.tls)
			if err := tc.Handshake(); err != nil {
				return
			}
			c, r, encrypted, user = tc, bufio.NewReader(tc), true, ""
		case "AUTH":
			mech, rest, _ := strings.Cut(arg, " ")
			var u, p string
			switch strings.ToUpper(mech) {
			case "PLAIN":
				if rest == "" {
					say("334 ")
					rest, _ = read()
				}
				raw, _ := base64.StdEncoding.DecodeString(rest)
				parts := strings.Split(string(raw), "\x00")
				if len(parts) == 3 {
					u, p = parts[1], parts[2]
				}
			case "LOGIN":
				say("334 VXNlcm5hbWU6")
				l, _ := read()
				ub, _ := base64.StdEncoding.DecodeString(l)
				say("334 UGFzc3dvcmQ6")
				l, _ = read()
				pb, _ := base64.StdEncoding.DecodeString(l)
				u, p = string(ub), string(pb)
			}
			if u == s.user && p == s.pass && u != "" {
				user = u
				say("235 ok")
			} else {
				say("535 bad credentials")
			}
		case "MAIL":
			if len(s.mechs) > 0 && user == "" {
				say("530 authenticate first")
				continue
			}
			from, rcpts = strings.Trim(strings.TrimPrefix(arg, "FROM:"), "<>"), nil
			say("250 ok")
		case "RCPT":
			to := strings.Trim(strings.TrimPrefix(arg, "TO:"), "<>")
			if strings.HasPrefix(to, "reject") {
				say("550 no such user")
				continue
			}
			rcpts = append(rcpts, to)
			say("250 ok")
		case "DATA":
			say("354 go ahead")
			var data strings.Builder
			for {
				l, ok := read()
				if !ok {
					return
				}
				if l == "." {
					break
				}
				data.WriteString(strings.TrimPrefix(l, ".") + "\r\n")
			}
			s.mu.Lock()
			s.got = append(s.got, delivery{from, rcpts, data.String(), encrypted, user})
			s.mu.Unlock()
			say("250 queued")
		case "QUIT":
			say("221 bye")
			return
		default:
			say("502 not implemented")
		}
	}
}

// testCertificate makes a certificate for 127.0.0.1 and localhost, writes
// it to dir, and answers the server's TLS config and the file's path.
func testCertificate(t *testing.T, dir string) (*tls.Config, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "nomi mail test"},
		NotBefore:             time.Now().Add(-time.Minute),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
		DNSNames:              []string{"localhost"},
		IPAddresses:           []net.IP{net.IPv4(127, 0, 0, 1)},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "cert.pem")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	return &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}}, path
}

const mailProgram = `import
    std::html.Html
    std::io
    std::mail.{self, Address, Attachment, Credentials, Mailer, Message, Security, SmtpConfig}
    std::tls.{ClientConfig, Roots}
end

fn show(answer: Result<Unit, mail.Error>) -> String
    case answer
        Ok(_) -> "ok"
        Err(e) -> "err ${e}"
    end
end

fn main()
    trusted = ClientConfig{roots: Roots.Files(["CERT"])}
    credentials = Some(Credentials{username: "user", password: "secret"})

    message = Message{
        from: Address.new("no-reply@example.com", name: "Example Updates")!,
        to: [Address.new("ana@example.com", name: "Zoë Ångström")!],
        cc: [Address.new("cc@example.com", name: "Lovelace, Ada")!],
        bcc: [Address.new("hidden@example.com")!],
        reply_to: [Address.new("reply@example.com")!],
        subject: "Grüße 🎉: a new update for Ada Lovelace, with a title long enough to fold",
        text: Some("Grüße,\n.hidden\nline with a trailing space \n${String.repeat("x", 120)}\n"),
        html: Some(Html"<p>Hello ${"Zoë & co"}</p>"),
        headers: [
            ("List-Unsubscribe", "<https://example.com/unsubscribe/abc>, <mailto:unsubscribe+abc@example.com>"),
            ("List-Unsubscribe-Post", "List-Unsubscribe=One-Click"),
        ],
        attachments: [
            Attachment{filename: "résumé.pdf", content_type: "application/pdf", data: String.to_bytes("%PDF-1.4 fake")},
        ],
    }

    starttls = SmtpConfig{host: "127.0.0.1", port: STARTTLS_PORT, credentials:, tls: trusted}
    io.print("starttls plain ${show(Mailer.send(mail.smtp(starttls), message))}")

    login = SmtpConfig{host: "localhost", port: LOGIN_PORT, credentials:, tls: trusted}
    io.print("starttls login ${show(Mailer.send(mail.smtp(login), message))}")

    implicit = SmtpConfig{host: "127.0.0.1", port: IMPLICIT_PORT, security: Security.Tls, credentials:, tls: trusted}
    io.print("implicit ${show(Mailer.send(mail.smtp(implicit), message))}")

    off = SmtpConfig{host: "127.0.0.1", port: OFF_PORT, security: Security.Off}
    io.print("off ${show(Mailer.send(mail.smtp(off), message))}")

    rejected = {..message, to: [Address.new("reject@example.com")!], cc: [], bcc: []}
    io.print("rejected ${show(Mailer.send(mail.smtp(off), rejected))}")

    wrong = Some(Credentials{username: "user", password: "wrong"})
    io.print("wrong password ${show(Mailer.send(mail.smtp({..starttls, credentials: wrong}), message))}")

    untrusted = {..starttls, tls: ClientConfig{}}
    io.print("untrusted ${show(Mailer.send(mail.smtp(untrusted), message))}")

    required = {..off, security: Security.StartTls}
    io.print("required ${show(Mailer.send(mail.smtp(required), message))}")
end
`

// std/mail's SMTP backend end to end against a fake server written here
// with Go's net and crypto/tls: STARTTLS with AUTH PLAIN and with AUTH
// LOGIN, TLS from the first byte, no TLS and no AUTH (a local catcher), a
// rejected recipient, a wrong password, an untrusted certificate and a
// server without STARTTLS when the config requires it. What arrives is
// read with Go's net/mail, mime and mime/multipart, which share nothing
// with std/mail's encoder.
func TestMail_SMTPEndToEnd(t *testing.T) {
	serverTLS, certPath := testCertificate(t, t.TempDir())
	starttls := startFakeSMTP(t, &fakeSMTP{tls: serverTLS, mechs: []string{"PLAIN", "LOGIN"}, user: "user", pass: "secret"})
	login := startFakeSMTP(t, &fakeSMTP{tls: serverTLS, mechs: []string{"LOGIN"}, user: "user", pass: "secret"})
	implicit := startFakeSMTP(t, &fakeSMTP{tls: serverTLS, implicit: true, mechs: []string{"PLAIN"}, user: "user", pass: "secret"})
	off := startFakeSMTP(t, &fakeSMTP{})

	src := strings.NewReplacer(
		`"CERT"`, fmt.Sprintf("%q", certPath),
		"STARTTLS_PORT", fmt.Sprint(starttls.port()),
		"LOGIN_PORT", fmt.Sprint(login.port()),
		"IMPLICIT_PORT", fmt.Sprint(implicit.port()),
		"OFF_PORT", fmt.Sprint(off.port()),
	).Replace(mailProgram)
	var errs bytes.Buffer
	p, err := LoadSource("main.nomi", src, WithErrorOutput(&errs))
	if err != nil {
		t.Fatalf("the front end rejects the program: %v\n%s", err, errs.String())
	}
	var out bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if err := p.Run(ctx, &out, nil, false); err != nil {
		t.Fatalf("run: %v\n%s\n%s", err, errs.String(), out.String())
	}
	got := out.String()
	for _, want := range []string{
		"starttls plain ok\n",
		"starttls login ok\n",
		"implicit ok\n",
		"off ok\n",
		"rejected err the mail server rejected RCPT TO <reject@example.com>: 550 no such user\n",
		"wrong password err the mail server rejected AUTH PLAIN: 535 bad credentials\n",
		"untrusted err sending mail: ",
		"required err mail server: 127.0.0.1 does not offer STARTTLS",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output lacks %q:\n%s", want, got)
		}
	}

	wantRcpts := []string{"ana@example.com", "cc@example.com", "hidden@example.com"}
	for _, c := range []struct {
		name   string
		server *fakeSMTP
		n      int
		tls    bool
		user   string
	}{
		{"starttls plain", starttls, 1, true, "user"},
		{"starttls login", login, 1, true, "user"},
		{"implicit", implicit, 1, true, "user"},
		{"off", off, 1, false, ""},
	} {
		ds := c.server.deliveries()
		if len(ds) != c.n {
			t.Fatalf("%s: %d deliveries, want %d (commands %v)", c.name, len(ds), c.n, c.server.commands())
		}
		d := ds[0]
		if d.tls != c.tls || d.user != c.user {
			t.Errorf("%s: tls %v user %q, want %v %q", c.name, d.tls, d.user, c.tls, c.user)
		}
		if d.from != "no-reply@example.com" || strings.Join(d.rcpts, " ") != strings.Join(wantRcpts, " ") {
			t.Errorf("%s: envelope %q -> %q", c.name, d.from, d.rcpts)
		}
		checkMessage(t, c.name, d.data)
	}
	if cmds := strings.Join(login.commands(), " "); !strings.Contains(cmds, "STARTTLS EHLO AUTH") {
		t.Errorf("the LOGIN server saw %s", cmds)
	}
	// The wrong password and the untrusted certificate stop before MAIL.
	if cmds := strings.Join(starttls.commands(), " "); strings.Count(cmds, "MAIL") != 1 {
		t.Errorf("the STARTTLS server saw %s", cmds)
	}
}

// checkMessage parses data with Go's mail packages and checks every part
// of the message mailProgram sends.
func checkMessage(t *testing.T, name, data string) {
	t.Helper()
	for i, line := range strings.Split(strings.TrimSuffix(data, "\r\n"), "\r\n") {
		if len(line) > 998 {
			t.Errorf("%s: line %d is %d bytes", name, i, len(line))
		}
		for _, b := range []byte(line) {
			if b >= 128 || (b < 32 && b != '\t') {
				t.Fatalf("%s: line %d holds byte %d: %q", name, i, b, line)
			}
		}
	}
	m, err := mail.ReadMessage(strings.NewReader(data))
	if err != nil {
		t.Fatalf("%s: net/mail cannot read the message: %v\n%s", name, err, data)
	}
	h := m.Header
	dec := new(mime.WordDecoder)
	if subject, err := dec.DecodeHeader(h.Get("Subject")); err != nil || subject != "Grüße 🎉: a new update for Ada Lovelace, with a title long enough to fold" {
		t.Errorf("%s: subject %q (%v)", name, subject, err)
	}
	if from, err := h.AddressList("From"); err != nil || len(from) != 1 || from[0].Name != "Example Updates" || from[0].Address != "no-reply@example.com" {
		t.Errorf("%s: From %v (%v)", name, from, err)
	}
	if to, err := h.AddressList("To"); err != nil || len(to) != 1 || to[0].Name != "Zoë Ångström" {
		t.Errorf("%s: To %v (%v)", name, to, err)
	}
	if cc, err := h.AddressList("Cc"); err != nil || len(cc) != 1 || cc[0].Name != "Lovelace, Ada" {
		t.Errorf("%s: Cc %v (%v)", name, cc, err)
	}
	if h.Get("Bcc") != "" || strings.Contains(data, "hidden@example.com") {
		t.Errorf("%s: the Bcc recipient is in the message", name)
	}
	if rt, err := h.AddressList("Reply-To"); err != nil || len(rt) != 1 || rt[0].Address != "reply@example.com" {
		t.Errorf("%s: Reply-To %v (%v)", name, rt, err)
	}
	if _, err := h.Date(); err != nil {
		t.Errorf("%s: Date: %v", name, err)
	}
	if id := h.Get("Message-Id"); !strings.HasPrefix(id, "<") || !strings.HasSuffix(id, "@example.com>") {
		t.Errorf("%s: Message-ID %q", name, id)
	}
	if got := h.Get("List-Unsubscribe"); got != "<https://example.com/unsubscribe/abc>, <mailto:unsubscribe+abc@example.com>" {
		t.Errorf("%s: List-Unsubscribe %q", name, got)
	}
	if h.Get("Mime-Version") != "1.0" {
		t.Errorf("%s: MIME-Version %q", name, h.Get("Mime-Version"))
	}

	kind, params, err := mime.ParseMediaType(h.Get("Content-Type"))
	if err != nil || kind != "multipart/mixed" {
		t.Fatalf("%s: Content-Type %q (%v)", name, h.Get("Content-Type"), err)
	}
	mixed := multipart.NewReader(m.Body, params["boundary"])
	alt, err := mixed.NextPart()
	if err != nil {
		t.Fatalf("%s: first part: %v", name, err)
	}
	altKind, altParams, _ := mime.ParseMediaType(alt.Header.Get("Content-Type"))
	if altKind != "multipart/alternative" {
		t.Fatalf("%s: first part is %q", name, altKind)
	}
	bodies := multipart.NewReader(alt, altParams["boundary"])
	wantText := "Grüße,\r\n.hidden\r\nline with a trailing space \r\n" + strings.Repeat("x", 120) + "\r\n"
	for _, want := range []struct{ kind, body string }{
		{"text/plain", wantText},
		{"text/html", "<p>Hello Zoë &amp; co</p>"},
	} {
		part, err := bodies.NextPart()
		if err != nil {
			t.Fatalf("%s: %s part: %v", name, want.kind, err)
		}
		k, ps, _ := mime.ParseMediaType(part.Header.Get("Content-Type"))
		body, _ := io.ReadAll(part)
		if k != want.kind || ps["charset"] != "utf-8" || string(body) != want.body {
			t.Errorf("%s: part %s %v %q, want %s %q", name, k, ps, body, want.kind, want.body)
		}
	}
	if _, err := bodies.NextPart(); err != io.EOF {
		t.Errorf("%s: multipart/alternative has a third part (%v)", name, err)
	}
	att, err := mixed.NextPart()
	if err != nil {
		t.Fatalf("%s: attachment: %v", name, err)
	}
	if att.FileName() != "résumé.pdf" || att.Header.Get("Content-Transfer-Encoding") != "base64" {
		t.Errorf("%s: attachment %q %v", name, att.FileName(), att.Header)
	}
	raw, _ := io.ReadAll(att)
	decoded, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(string(raw), "\r\n", ""))
	if err != nil || string(decoded) != "%PDF-1.4 fake" {
		t.Errorf("%s: attachment body %q (%v)", name, decoded, err)
	}
	if _, err := mixed.NextPart(); err != io.EOF {
		t.Errorf("%s: multipart/mixed has a third part (%v)", name, err)
	}
}

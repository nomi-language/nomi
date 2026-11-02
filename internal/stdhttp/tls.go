package stdhttp

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/nomi-language/nomi/rt"
)

// std/tls's Go side. A ClientConfig or ServerConfig is a Nomi value; each
// use builds Go's *tls.Config from its parts (TLSConfig, then
// TLSWithCertificate per certificate), so nothing a program holds changes.
// A certificate is a handle: its private key never crosses back. An Err is a
// tls.Error variant's name and its fields, as http_host's errors are.

// certificate is what std/http_host's `Certificate` wraps.
type certificate struct {
	c    tls.Certificate
	leaf *x509.Certificate
}

func certificateOf(c rt.TLSCertificate) *certificate {
	x, ok := c.Value().(*certificate)
	if !ok {
		rt.Trap("tls: a Certificate holds no certificate")
	}
	return x
}

// tlsConf is what std/http_host's `TlsConfig` wraps: Go's configuration,
// and the broker its hooks (certificate_for, verify, key_log) run through.
// The listener, client or handshake that uses the configuration sets b to
// the broker it serves while Go works.
type tlsConf struct {
	c *tls.Config
	b *broker
}

func confOf(c rt.TLSConfig) *tlsConf {
	x, ok := c.Value().(*tlsConf)
	if !ok {
		rt.Trap("tls: a TlsConfig holds no configuration")
	}
	return x
}

func configOf(c rt.TLSConfig) *tls.Config { return confOf(c).c }

// hook runs f through the configuration's broker, waiting for it until
// stop closes; false when no task serves the broker.
func (t *tlsConf) hook(stop <-chan struct{}, f func(*rt.Frame)) bool {
	if t.b == nil {
		return false
	}
	return t.b.call(stop, f)
}

func newCertificate(c tls.Certificate, err error) rt.Result[rt.TLSCertificate, *rt.List[string]] {
	if err != nil {
		return rt.Err[rt.TLSCertificate](listOf([]string{"Certificate", err.Error()}))
	}
	leaf := c.Leaf
	if leaf == nil && len(c.Certificate) > 0 {
		if leaf, err = x509.ParseCertificate(c.Certificate[0]); err != nil {
			return rt.Err[rt.TLSCertificate](listOf([]string{"Certificate", err.Error()}))
		}
	}
	return rt.Ok[rt.TLSCertificate, *rt.List[string]](rt.NewTLSCertificate(&certificate{c: c, leaf: leaf}))
}

// CertificateFromPEM is `http_host.certificate_from_pem`: Go's X509KeyPair.
func CertificateFromPEM(cert, key string) rt.Result[rt.TLSCertificate, *rt.List[string]] {
	return newCertificate(tls.X509KeyPair([]byte(cert), []byte(key)))
}

// CertificateLoad is `http_host.certificate_load`: Go's LoadX509KeyPair.
func CertificateLoad(certPath, keyPath string) rt.Result[rt.TLSCertificate, *rt.List[string]] {
	return newCertificate(tls.LoadX509KeyPair(certPath, keyPath))
}

// CertificateInfo is `http_host.certificate_info`: the leaf's parts, as
// certInfo lists them.
func CertificateInfo(c rt.TLSCertificate) *rt.List[string] {
	return listOf(certInfo(certificateOf(c).leaf))
}

// certInfo is a certificate's subject, issuer, serial number, validity
// (Unix nanoseconds) and DNS names, in that order, for std/tls's
// CertificateInfo. A nil certificate is empty parts.
func certInfo(x *x509.Certificate) []string {
	if x == nil {
		return []string{"", "", "", "0", "0"}
	}
	out := []string{x.Subject.String(), x.Issuer.String(), x.SerialNumber.String(),
		strconv.FormatInt(x.NotBefore.UnixNano(), 10), strconv.FormatInt(x.NotAfter.UnixNano(), 10)}
	return append(out, x.DNSNames...)
}

func certInfos(xs []*x509.Certificate) *rt.List[*rt.List[string]] {
	var out *rt.List[*rt.List[string]]
	for i := len(xs) - 1; i >= 0; i-- {
		out = rt.Cons(listOf(certInfo(xs[i])), out)
	}
	return out
}

// tlsVersion is Go's version for std/tls's 10, 11, 12 or 13; 0 for 0.
func tlsVersion(v int64) uint16 {
	switch v {
	case 10:
		return tls.VersionTLS10
	case 11:
		return tls.VersionTLS11
	case 12:
		return tls.VersionTLS12
	case 13:
		return tls.VersionTLS13
	}
	return 0
}

// versionNumber is tlsVersion's inverse.
func versionNumber(v uint16) int64 {
	switch v {
	case tls.VersionTLS10:
		return 10
	case tls.VersionTLS11:
		return 11
	case tls.VersionTLS12:
		return 12
	case tls.VersionTLS13:
		return 13
	}
	return 0
}

var curveNames = map[string]tls.CurveID{
	"X25519":         tls.X25519,
	"P256":           tls.CurveP256,
	"P384":           tls.CurveP384,
	"P521":           tls.CurveP521,
	"X25519MlKem768": tls.X25519MLKEM768,
	"P256MlKem768":   tls.SecP256r1MLKEM768,
	"P384MlKem1024":  tls.SecP384r1MLKEM1024,
	"MlKem1024":      tls.MLKEM1024,
}

func curveName(id tls.CurveID) string {
	for name, c := range curveNames {
		if c == id {
			return name
		}
	}
	return ""
}

// roots is the pool std/tls's Roots names: nil (the system's) for kind 0,
// the PEM text for 1, the PEM files for 2.
func roots(kind int64, pem string, files *rt.List[string]) (*x509.CertPool, []string) {
	switch kind {
	case 1:
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM([]byte(pem)) {
			return nil, []string{"Certificate", "the roots' PEM text holds no certificate"}
		}
		return pool, nil
	case 2:
		pool := x509.NewCertPool()
		for _, f := range strsOf(files) {
			data, err := os.ReadFile(f)
			if err != nil {
				return nil, []string{"Certificate", err.Error()}
			}
			if !pool.AppendCertsFromPEM(data) {
				return nil, []string{"Certificate", f + " holds no PEM certificate"}
			}
		}
		return pool, nil
	}
	return nil, nil
}

// TLSConfig is `http_host.tls_config`: Go's configuration for one side.
// Versions are 10 to 13 and 0 for Go's default; ciphers and curves apply
// when their flag is set; renegotiation is 0 never, 1 once, 2 freely;
// clientAuth is Go's ClientAuthType in order.
func TLSConfig(server bool, rootsKind int64, rootsPEM string, rootsFiles *rt.List[string], serverName string,
	insecure bool, nextProtos *rt.List[string], minVersion, maxVersion int64, ciphers *rt.List[int64],
	hasCiphers bool, curves *rt.List[string], hasCurves bool, sessionTickets bool, sessionCache int64,
	renegotiation, clientAuth int64,
	certificateFor func(*rt.Frame, rt.HTTPHandle) rt.Maybe[rt.TLSCertificate], hasCertificateFor bool,
	verify func(*rt.Frame, *rt.List[*rt.List[string]]) string, hasVerify bool,
	keyLog rt.Writer, hasKeyLog bool) rt.Result[rt.TLSConfig, *rt.List[string]] {
	pool, fail := roots(rootsKind, rootsPEM, rootsFiles)
	if fail != nil {
		return rt.Err[rt.TLSConfig](listOf(fail))
	}
	c := &tls.Config{
		ServerName:             serverName,
		InsecureSkipVerify:     insecure,
		NextProtos:             strsOf(nextProtos),
		MinVersion:             tlsVersion(minVersion),
		MaxVersion:             tlsVersion(maxVersion),
		SessionTicketsDisabled: !sessionTickets,
		Renegotiation:          tls.RenegotiationSupport(renegotiation),
	}
	if server {
		c.ClientCAs = pool
		c.ClientAuth = tls.ClientAuthType(clientAuth)
	} else {
		c.RootCAs = pool
	}
	if hasCiphers {
		c.CipherSuites = []uint16{}
		for l := ciphers; l != nil; l = l.Tail {
			c.CipherSuites = append(c.CipherSuites, uint16(l.Head))
		}
	}
	if hasCurves {
		c.CurvePreferences = []tls.CurveID{}
		for _, name := range strsOf(curves) {
			c.CurvePreferences = append(c.CurvePreferences, curveNames[name])
		}
	}
	if sessionCache > 0 {
		c.ClientSessionCache = tls.NewLRUClientSessionCache(int(sessionCache))
	}
	conf := &tlsConf{c: c}
	if hasCertificateFor {
		c.GetCertificate = func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
			var chosen *tls.Certificate
			ran := conf.hook(hello.Context().Done(), func(fr *rt.Frame) {
				if m := certificateFor(fr, rt.NewHTTPHandle(hello)); m.Tag == rt.TagSome {
					cert := certificateOf(m.Some).c
					chosen = &cert
				}
			})
			if !ran {
				return nil, errors.New("tls: no task ran certificate_for")
			}
			return chosen, nil
		}
	}
	if hasVerify {
		c.VerifyConnection = func(s tls.ConnectionState) error {
			reason := ""
			if !conf.hook(nil, func(fr *rt.Frame) { reason = verify(fr, stateParts(s)) }) {
				return errors.New("tls: no task ran verify")
			}
			if reason != "" {
				return errors.New(reason)
			}
			return nil
		}
	}
	if hasKeyLog {
		c.KeyLogWriter = keyLogWriter{conf: conf, w: keyLog}
	}
	return rt.Ok[rt.TLSConfig, *rt.List[string]](rt.NewTLSConfig(conf))
}

// keyLogWriter hands Go's key log lines to the program's Writer through
// the configuration's broker.
type keyLogWriter struct {
	conf *tlsConf
	w    rt.Writer
}

func (k keyLogWriter) Write(p []byte) (int, error) {
	if k.conf.b == nil {
		return len(p), nil
	}
	line := rt.Bytes(append([]byte(nil), p...))
	k.conf.b.post(func(fr *rt.Frame) {
		_ = rt.WriterWrite(fr, k.w, line)
		_ = rt.WriterFlush(fr, k.w)
	})
	return len(p), nil
}

// stateParts is a connection state for std/tls's ConnectionState: the
// version, handshake_complete, resumed, cipher suite, curve, protocol and
// server name; the local certificate's info (empty for none); the peer's
// certificates, each certInfo joined by newlines; then each verified chain
// the same way.
func stateParts(s tls.ConnectionState) *rt.List[*rt.List[string]] {
	head := []string{strconv.FormatInt(versionNumber(s.Version), 10), strconv.FormatBool(s.HandshakeComplete),
		strconv.FormatBool(s.DidResume), strconv.Itoa(int(s.CipherSuite)), curveName(s.CurveID), s.NegotiatedProtocol, s.ServerName}
	var local []string
	if len(s.LocalCertificate) > 0 {
		if x, err := x509.ParseCertificate(s.LocalCertificate[0]); err == nil {
			local = certInfo(x)
		}
	}
	joined := func(xs []*x509.Certificate) []string {
		out := []string{}
		for _, x := range xs {
			out = append(out, strings.Join(certInfo(x), "\n"))
		}
		return out
	}
	parts := []*rt.List[string]{listOf(head), listOf(local), listOf(joined(s.PeerCertificates))}
	for _, chain := range s.VerifiedChains {
		parts = append(parts, listOf(joined(chain)))
	}
	var out *rt.List[*rt.List[string]]
	for i := len(parts) - 1; i >= 0; i-- {
		out = rt.Cons(parts[i], out)
	}
	return out
}

func helloOf(h rt.HTTPHandle) *tls.ClientHelloInfo {
	x, ok := h.Value().(*tls.ClientHelloInfo)
	if !ok {
		rt.Trap("tls: a ClientHello holds no hello")
	}
	return x
}

// HelloParts is `http_host.hello_parts`: a ClientHello's server name, the
// protocols it offers, its versions (10 to 13) and its cipher suite IDs.
func HelloParts(h rt.HTTPHandle) *rt.List[*rt.List[string]] {
	x := helloOf(h)
	var versions, suites []string
	for _, v := range x.SupportedVersions {
		if n := versionNumber(v); n != 0 {
			versions = append(versions, strconv.FormatInt(n, 10))
		}
	}
	for _, c := range x.CipherSuites {
		suites = append(suites, strconv.Itoa(int(c)))
	}
	return rt.Cons(listOf([]string{x.ServerName}), rt.Cons(listOf(x.SupportedProtos),
		rt.Cons(listOf(versions), rt.Cons(listOf(suites), (*rt.List[*rt.List[string]])(nil)))))
}

// HelloSupports is `http_host.hello_supports`: "" when the client can use
// cert, as Go's SupportsCertificate finds, else why not.
func HelloSupports(h rt.HTTPHandle, cert rt.TLSCertificate) string {
	c := certificateOf(cert).c
	if err := helloOf(h).SupportsCertificate(&c); err != nil {
		return err.Error()
	}
	return ""
}

// TLSWithCertificate is `http_host.tls_with_certificate`: adds cert to the
// certificates the configuration presents. The configuration is std/tls's
// own, made for this use, so adding to it changes nothing a program holds.
func TLSWithCertificate(c rt.TLSConfig, cert rt.TLSCertificate) rt.TLSConfig {
	cfg := configOf(c)
	cfg.Certificates = append(cfg.Certificates, certificateOf(cert).c)
	return c
}

// CipherSuites is `http_host.cipher_suites`: Go's CipherSuites, or its
// InsecureCipherSuites, each as its ID, name, whether it is insecure, then
// the versions (10 to 13) it supports.
func CipherSuites(insecure bool) *rt.List[*rt.List[string]] {
	suites := tls.CipherSuites()
	if insecure {
		suites = tls.InsecureCipherSuites()
	}
	var out *rt.List[*rt.List[string]]
	for i := len(suites) - 1; i >= 0; i-- {
		s := suites[i]
		parts := []string{strconv.Itoa(int(s.ID)), s.Name, strconv.FormatBool(s.Insecure)}
		for _, v := range s.SupportedVersions {
			parts = append(parts, strconv.FormatInt(versionNumber(v), 10))
		}
		out = rt.Cons(listOf(parts), out)
	}
	return out
}

// CipherSuiteName is `http_host.cipher_suite_name`: Go's CipherSuiteName.
func CipherSuiteName(id int64) string { return tls.CipherSuiteName(uint16(id)) }

// tlsErrorParts is a TLS failure as a tls.Error variant's parts.
func tlsErrorParts(err error) []string {
	var alert tls.AlertError
	var verify *tls.CertificateVerificationError
	var record tls.RecordHeaderError
	switch {
	case asError(err, &alert):
		return []string{"Alert", strconv.Itoa(int(alert)), err.Error()}
	case asError(err, &verify):
		parts := []string{"Verification", verify.Err.Error()}
		for _, c := range verify.UnverifiedCertificates {
			parts = append(parts, strings.Join(certInfo(c), "\n"))
		}
		return parts
	case asError(err, &record):
		return []string{"NotTls", record.Msg}
	}
	return []string{"Handshake", err.Error()}
}

// TestCertificate is `http_host.test_certificate`: a self-signed
// certificate for 127.0.0.1, ::1 and localhost, valid for a day, and its
// key, as PEM text: what Go's httptest serves TLS with, made fresh.
func TestCertificate() *rt.List[string] {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		rt.Trap("tls: " + err.Error())
	}
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(now.UnixNano()),
		Subject:               pkix.Name{Organization: []string{"Nomi test server"}},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
		DNSNames:              []string{"localhost"},
		IPAddresses:           []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		rt.Trap("tls: " + err.Error())
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		rt.Trap("tls: " + err.Error())
	}
	return listOf([]string{
		string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})),
		string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})),
	})
}

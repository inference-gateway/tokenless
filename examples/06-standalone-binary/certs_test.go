// Throwaway PKI generated in-process for the TLS and mTLS binary tests.
package main

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	require "github.com/stretchr/testify/require"
)

// writeCerts generates a server certificate (self-signed, so it doubles as
// the trust root for the test client) and a client CA with a signed client
// certificate for mTLS, all in dir. Returns the file paths.
func writeCerts(t *testing.T, dir string) (serverCert, serverKey, clientCA, clientCert, clientKey string) {
	t.Helper()
	srvCrt, srvKey := genCert(t, "tokenless-server", []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")}, nil, nil)
	caCrt, caKey := genCert(t, "tokenless-client-ca", nil, nil, nil)
	cliCrt, cliKey := genCert(t, "tokenless-client", nil, caCrt, caKey)
	return writeFile(t, dir, "server.crt", certPEM(srvCrt)),
		writeFile(t, dir, "server.key", keyPEM(t, srvKey)),
		writeFile(t, dir, "client-ca.crt", certPEM(caCrt)),
		writeFile(t, dir, "client.crt", certPEM(cliCrt)),
		writeFile(t, dir, "client.key", keyPEM(t, cliKey))
}

// genCert mints a P-256 certificate; parent == nil means self-signed and
// usable as a CA (that is all the test trust roots need).
func genCert(t *testing.T, cn string, ips []net.IP, parent *x509.Certificate, parentKey crypto.Signer) (*x509.Certificate, crypto.Signer) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	tpl := x509.Certificate{
		Subject:               pkix.Name{CommonName: cn},
		SerialNumber:          big.NewInt(1),
		NotBefore:             time.Now().Add(-time.Minute),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
		IsCA:                  parent == nil,
		IPAddresses:           ips,
	}
	issuer, signer := parent, crypto.Signer(key)
	if parent == nil {
		issuer = &tpl // self-signed
	} else {
		signer = parentKey
	}
	der, err := x509.CreateCertificate(rand.Reader, &tpl, issuer, key.Public(), signer)
	require.NoError(t, err)
	cert, err := x509.ParseCertificate(der)
	require.NoError(t, err)
	return cert, key
}

func certPEM(cert *x509.Certificate) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw})
}

func keyPEM(t *testing.T, key crypto.Signer) []byte {
	t.Helper()
	der, err := x509.MarshalPKCS8PrivateKey(key)
	require.NoError(t, err)
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
}

func writeFile(t *testing.T, dir, name string, content []byte) string {
	t.Helper()
	path := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(path, content, 0o600))
	return path
}

package main

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"time"
)

type certificateAuthority struct {
	cert    *x509.Certificate
	certPEM []byte
	key     crypto.Signer
}

func loadCertificateAuthority(certFile, keyFile string) (*certificateAuthority, error) {
	certPEM, err := os.ReadFile(certFile)
	if err != nil {
		return nil, fmt.Errorf("read agent CA certificate: %w", err)
	}
	block, _ := pem.Decode(certPEM)
	if block == nil {
		return nil, fmt.Errorf("agent CA certificate is not PEM")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse agent CA certificate: %w", err)
	}
	if !cert.IsCA {
		return nil, fmt.Errorf("agent CA certificate is not a CA")
	}
	keyPEM, err := os.ReadFile(keyFile)
	if err != nil {
		return nil, fmt.Errorf("read agent CA key: %w", err)
	}
	keyBlock, _ := pem.Decode(keyPEM)
	if keyBlock == nil {
		return nil, fmt.Errorf("agent CA key is not PEM")
	}
	key, err := parseSigner(keyBlock.Bytes)
	if err != nil {
		return nil, err
	}
	return &certificateAuthority{cert: cert, certPEM: certPEM, key: key}, nil
}

func parseSigner(der []byte) (crypto.Signer, error) {
	if value, err := x509.ParsePKCS8PrivateKey(der); err == nil {
		switch key := value.(type) {
		case *rsa.PrivateKey:
			return key, nil
		case *ecdsa.PrivateKey:
			return key, nil
		case ed25519.PrivateKey:
			return key, nil
		}
	}
	if key, err := x509.ParsePKCS1PrivateKey(der); err == nil {
		return key, nil
	}
	if key, err := x509.ParseECPrivateKey(der); err == nil {
		return key, nil
	}
	return nil, fmt.Errorf("unsupported agent CA private key")
}

func (ca *certificateAuthority) signCSR(csrPEM, agentID string) (string, string, time.Time, error) {
	block, _ := pem.Decode([]byte(csrPEM))
	if block == nil {
		return "", "", time.Time{}, fmt.Errorf("CSR is not PEM")
	}
	csr, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil {
		return "", "", time.Time{}, fmt.Errorf("parse CSR: %w", err)
	}
	if err := csr.CheckSignature(); err != nil {
		return "", "", time.Time{}, fmt.Errorf("invalid CSR signature: %w", err)
	}
	serialLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serial, err := rand.Int(rand.Reader, serialLimit)
	if err != nil {
		return "", "", time.Time{}, err
	}
	now := time.Now().UTC()
	expires := now.Add(30 * 24 * time.Hour)
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject:      csr.Subject,
		NotBefore:    now.Add(-time.Minute), NotAfter: expires,
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
	}
	template.Subject.CommonName = agentID
	der, err := x509.CreateCertificate(rand.Reader, template, ca.cert, csr.PublicKey, ca.key)
	if err != nil {
		return "", "", time.Time{}, err
	}
	out := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	return string(out), serial.String(), expires, nil
}

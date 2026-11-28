// Package pgpkit encrypts, signs, decrypts and verifies text with OpenPGP and
// ASCII armor, so a message can pass through a system that must not read it
// (an automation tool, a log, a chat) and still be checked on the far side.
//
// It is a small wrapper over github.com/ProtonMail/go-crypto/openpgp with
// strict defaults: keys are ed25519 and cv25519, messages use AES-256 with the
// library's integrity check, a message that fails the check returns an error
// and never partial plaintext, and input is capped at MaxSize.
//
// Private keys can be passphrase protected: pass WithPassphrase to GenerateKey
// to protect a new key, and to the other functions to unlock one. Limits: there
// is no key rotation, expiry handling or revocation, and key storage and
// passphrase handling are the caller's job.
// Encryption hides the text from whoever carries it. It does not protect it
// from the holder of the private key.
package pgpkit

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/armor"
	"github.com/ProtonMail/go-crypto/openpgp/clearsign"
	"github.com/ProtonMail/go-crypto/openpgp/packet"
)

// MaxSize is the largest plaintext Encrypt, Decrypt, ClearSign and
// VerifyClearSigned accept, in bytes.
const MaxSize = 1 << 20

// Key is an armored OpenPGP key pair. Keep Private secret. Public is safe to share.
type Key struct {
	Private     string
	Public      string
	Fingerprint string
}

// Result is what Decrypt returns. Signed is true only when the message carried
// a signature that verified against the public key passed to Decrypt.
type Result struct {
	Plaintext         string
	Signed            bool
	SignerFingerprint string
}

// Option changes how a function treats keys.
type Option func(*options)

type options struct{ passphrase string }

// WithPassphrase protects a key made by GenerateKey, or unlocks a protected
// private key passed to Encrypt, Decrypt or ClearSign.
func WithPassphrase(p string) Option { return func(o *options) { o.passphrase = p } }

func opts(list []Option) options {
	var o options
	for _, f := range list {
		f(&o)
	}
	return o
}

func config() *packet.Config {
	return &packet.Config{DefaultCipher: packet.CipherAES256}
}

// GenerateKey makes a new ed25519 signing key with a cv25519 encryption subkey.
func GenerateKey(name, comment, email string, options ...Option) (Key, error) {
	o := opts(options)
	e, err := openpgp.NewEntity(name, comment, email, &packet.Config{
		DefaultCipher: packet.CipherAES256,
		Algorithm:     packet.PubKeyAlgoEdDSA,
		Curve:         packet.Curve25519,
	})
	if err != nil {
		return Key{}, err
	}
	// Serialize the public half first: encrypting the private key below must not
	// change what the public key says.
	var priv, pub bytes.Buffer
	w, err := armor.Encode(&pub, openpgp.PublicKeyType, nil)
	if err != nil {
		return Key{}, err
	}
	if err := e.Serialize(w); err != nil {
		return Key{}, err
	}
	if err := w.Close(); err != nil {
		return Key{}, err
	}
	if o.passphrase != "" {
		if err := e.EncryptPrivateKeys([]byte(o.passphrase), config()); err != nil {
			return Key{}, err
		}
	}
	w, err = armor.Encode(&priv, openpgp.PrivateKeyType, nil)
	if err != nil {
		return Key{}, err
	}
	// SerializePrivate signs again, which needs the unlocked key, so a protected
	// key is written as it is.
	if o.passphrase != "" {
		err = e.SerializePrivateWithoutSigning(w, nil)
	} else {
		err = e.SerializePrivate(w, nil)
	}
	if err != nil {
		return Key{}, err
	}
	if err := w.Close(); err != nil {
		return Key{}, err
	}
	return Key{Private: priv.String(), Public: pub.String(), Fingerprint: fingerprint(e)}, nil
}

func fingerprint(e *openpgp.Entity) string {
	return fmt.Sprintf("%X", e.PrimaryKey.Fingerprint)
}

func readRing(armored, what string) (openpgp.EntityList, error) {
	ring, err := openpgp.ReadArmoredKeyRing(strings.NewReader(armored))
	if err != nil {
		return nil, fmt.Errorf("%s is not a valid armored OpenPGP key", what)
	}
	if len(ring) == 0 {
		return nil, fmt.Errorf("%s holds no key", what)
	}
	return ring, nil
}

func readPrivate(armored, what, passphrase string) (openpgp.EntityList, error) {
	ring, err := readRing(armored, what)
	if err != nil {
		return nil, err
	}
	for _, e := range ring {
		if e.PrivateKey == nil {
			return nil, fmt.Errorf("%s has no private key", what)
		}
		if e.PrivateKey.Encrypted {
			if passphrase == "" {
				return nil, fmt.Errorf("%s is passphrase protected: pass WithPassphrase", what)
			}
			if err := e.DecryptPrivateKeys([]byte(passphrase)); err != nil {
				return nil, fmt.Errorf("%s: wrong passphrase", what)
			}
		}
	}
	return ring, nil
}

func tooLarge(s string) error {
	if len(s) > MaxSize {
		return fmt.Errorf("text is %d bytes, over the %d byte limit", len(s), MaxSize)
	}
	return nil
}

// Encrypt encrypts plaintext to recipientPublic and returns ASCII armor
// ("-----BEGIN PGP MESSAGE-----"). If signerPrivate is not empty the message is
// also signed, so the receiver can prove who sent it.
func Encrypt(recipientPublic, plaintext, signerPrivate string, options ...Option) (string, error) {
	o := opts(options)
	if err := tooLarge(plaintext); err != nil {
		return "", err
	}
	to, err := readRing(recipientPublic, "recipient key")
	if err != nil {
		return "", err
	}
	var signer *openpgp.Entity
	if signerPrivate != "" {
		ring, err := readPrivate(signerPrivate, "signer key", o.passphrase)
		if err != nil {
			return "", err
		}
		signer = ring[0]
	}
	var buf bytes.Buffer
	aw, err := armor.Encode(&buf, "PGP MESSAGE", nil)
	if err != nil {
		return "", err
	}
	w, err := openpgp.Encrypt(aw, to, signer, nil, config())
	if err != nil {
		return "", err
	}
	if _, err := io.WriteString(w, plaintext); err != nil {
		return "", err
	}
	if err := w.Close(); err != nil {
		return "", err
	}
	if err := aw.Close(); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// Decrypt decrypts an armored message with privateKey. If signerPublic is not
// empty the message must be signed by that key, and a missing or bad signature
// is an error. The whole message is read and checked before anything is
// returned.
func Decrypt(privateKey, armored, signerPublic string, options ...Option) (Result, error) {
	o := opts(options)
	if err := tooLarge(armored); err != nil {
		return Result{}, err
	}
	ring, err := readPrivate(privateKey, "private key", o.passphrase)
	if err != nil {
		return Result{}, err
	}
	keys := ring
	if signerPublic != "" {
		pub, err := readRing(signerPublic, "signer key")
		if err != nil {
			return Result{}, err
		}
		keys = append(append(openpgp.EntityList{}, ring...), pub...)
	}
	blk, err := armor.Decode(strings.NewReader(armored))
	if err != nil || blk.Type != "PGP MESSAGE" {
		return Result{}, errors.New("input is not an armored PGP message")
	}
	md, err := openpgp.ReadMessage(blk.Body, keys, nil, config())
	if err != nil {
		return Result{}, errors.New("message was not encrypted to this key")
	}
	b, err := io.ReadAll(io.LimitReader(md.UnverifiedBody, MaxSize+1))
	if err != nil {
		return Result{}, errors.New("message failed its integrity check")
	}
	if len(b) > MaxSize {
		return Result{}, errors.New("decrypted text is over the size limit")
	}
	res := Result{Plaintext: string(b)}
	if signerPublic == "" {
		return res, nil
	}
	if !md.IsSigned {
		return Result{}, errors.New("message is not signed")
	}
	if md.SignatureError != nil || md.SignedBy == nil {
		return Result{}, errors.New("signature did not verify")
	}
	want := make(map[string]bool)
	pub, _ := readRing(signerPublic, "signer key")
	for _, e := range pub {
		want[fingerprint(e)] = true
	}
	got := fmt.Sprintf("%X", md.SignedBy.PublicKey.Fingerprint)
	if !want[got] {
		// The signer may have signed with a subkey, so also accept a match on the owning key.
		matched := false
		for _, e := range pub {
			for _, sk := range e.Subkeys {
				if fmt.Sprintf("%X", sk.PublicKey.Fingerprint) == got {
					matched, got = true, fingerprint(e)
				}
			}
		}
		if !matched {
			return Result{}, errors.New("message was signed by a different key")
		}
	}
	res.Signed, res.SignerFingerprint = true, got
	return res, nil
}

// ClearSign signs text and returns it with the signature in ASCII armor
// ("-----BEGIN PGP SIGNED MESSAGE-----"). The text stays readable.
func ClearSign(privateKey, text string, options ...Option) (string, error) {
	o := opts(options)
	if err := tooLarge(text); err != nil {
		return "", err
	}
	ring, err := readPrivate(privateKey, "private key", o.passphrase)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	w, err := clearsign.Encode(&buf, ring[0].PrivateKey, config())
	if err != nil {
		return "", err
	}
	if _, err := io.WriteString(w, text); err != nil {
		return "", err
	}
	if err := w.Close(); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// VerifyClearSigned checks a clear-signed message against publicKey and returns
// the signed text. Any text outside the signed block is an error, so a reader
// cannot be shown unsigned words next to a valid signature.
func VerifyClearSigned(publicKey, signed string) (string, error) {
	if err := tooLarge(signed); err != nil {
		return "", err
	}
	ring, err := readRing(publicKey, "public key")
	if err != nil {
		return "", err
	}
	blk, rest := clearsign.Decode([]byte(signed))
	if blk == nil {
		return "", errors.New("input is not a clear-signed message")
	}
	start := strings.Index(signed, "-----BEGIN PGP SIGNED MESSAGE-----")
	if start < 0 || len(bytes.TrimSpace([]byte(signed[:start]))) != 0 || len(bytes.TrimSpace(rest)) != 0 {
		return "", errors.New("unsigned text surrounds the signed block")
	}
	if _, err := openpgp.CheckDetachedSignature(ring, bytes.NewReader(blk.Bytes), blk.ArmoredSignature.Body, config()); err != nil {
		return "", errors.New("signature did not verify")
	}
	return string(blk.Plaintext), nil
}

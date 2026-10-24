package intent

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// Vault holds one tenant's rules. Every change bumps Version, so a decision
// can say which policy state it was made under. Safe for concurrent use.
type Vault struct {
	mu      sync.RWMutex
	tenant  string
	version int
	rules   map[string]*Rule
	now     func() time.Time
}

// NewVault returns an empty vault for tenant. now may be nil.
func NewVault(tenant string, now func() time.Time) *Vault {
	if now == nil {
		now = time.Now
	}
	return &Vault{tenant: tenant, rules: map[string]*Rule{}, now: now}
}

// Tenant is the tenant this vault belongs to.
func (v *Vault) Tenant() string { return v.tenant }

// Version is the current policy version.
func (v *Vault) Version() int {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.version
}

// Add stores a new rule. The rule is explicit: Provenance defaults to the
// owner writing it now. An id that already exists is refused; use Edit.
func (v *Vault) Add(r Rule) error {
	if r.Provenance.Source == "" {
		r.Provenance = Provenance{Source: "explicit", By: r.Owner}
	}
	if err := r.validate(); err != nil {
		return err
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if _, ok := v.rules[r.ID]; ok {
		return fmt.Errorf("intent: rule %q already exists", r.ID)
	}
	v.version++
	r.CreatedAt = v.now().UTC()
	if r.Provenance.At.IsZero() {
		r.Provenance.At = r.CreatedAt
	}
	r.Version = v.version
	r.Revoked, r.RevokedAt = false, time.Time{}
	v.rules[r.ID] = &r
	return nil
}

// Edit replaces a rule's scope, requirements, label, preference, purpose and
// expiry. Identity, owner, provenance and creation time are kept. A revoked
// rule cannot be edited: add a new one so the history stays honest.
func (v *Vault) Edit(r Rule) error {
	if err := r.validate(); err != nil {
		return err
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	old, ok := v.rules[r.ID]
	if !ok {
		return fmt.Errorf("intent: no rule %q", r.ID)
	}
	if old.Revoked {
		return fmt.Errorf("intent: rule %q is revoked, add a new rule instead", r.ID)
	}
	v.version++
	r.Owner, r.Provenance, r.CreatedAt = old.Owner, old.Provenance, old.CreatedAt
	r.Version = v.version
	r.Revoked = false
	v.rules[r.ID] = &r
	return nil
}

// Revoke deactivates a rule at once. The record stays so the history shows it.
func (v *Vault) Revoke(id string) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	r, ok := v.rules[id]
	if !ok {
		return fmt.Errorf("intent: no rule %q", id)
	}
	if r.Revoked {
		return nil
	}
	v.version++
	r.Revoked, r.RevokedAt, r.Version = true, v.now().UTC(), v.version
	return nil
}

// Delete removes a rule and its private preference for good. Revoke keeps a
// record; Delete is the retention control for when the owner wants it gone.
func (v *Vault) Delete(id string) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if _, ok := v.rules[id]; !ok {
		return fmt.Errorf("intent: no rule %q", id)
	}
	v.version++
	delete(v.rules, id)
	return nil
}

// Get returns a copy of one rule.
func (v *Vault) Get(id string) (Rule, bool) {
	v.mu.RLock()
	defer v.mu.RUnlock()
	r, ok := v.rules[id]
	if !ok {
		return Rule{}, false
	}
	return *r, true
}

// List returns copies of every rule, sorted by id.
func (v *Vault) List() []Rule {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.listLocked()
}

func (v *Vault) listLocked() []Rule {
	out := make([]Rule, 0, len(v.rules))
	for _, r := range v.rules {
		out = append(out, *r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// snapshot returns the active rules and the digest and version of the policy
// they form, taken under one lock so they cannot disagree.
func (v *Vault) snapshot(now time.Time) (rules []Rule, digest string, version int) {
	v.mu.RLock()
	defer v.mu.RUnlock()
	all := v.listLocked()
	views := make([]publicView, 0, len(all))
	for i := range all {
		views = append(views, all[i].public())
		if all[i].active(now) {
			rules = append(rules, all[i])
		}
	}
	b, _ := json.Marshal(struct {
		Tenant string       `json:"tenant"`
		Rules  []publicView `json:"rules"`
	}{v.tenant, views})
	sum := sha256.Sum256(append([]byte("joltrin-intent/policy/v1\n"), b...))
	return rules, hex.EncodeToString(sum[:]), v.version
}

// Export returns the whole vault as JSON, private preferences included. It is
// the owner's portable copy.
func (v *Vault) Export() ([]byte, error) {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return json.MarshalIndent(vaultFile{Tenant: v.tenant, Version: v.version, Rules: v.listLocked()}, "", "  ")
}

// Import loads an Export. It replaces the vault's contents.
func (v *Vault) Import(b []byte) error {
	var f vaultFile
	if err := json.Unmarshal(b, &f); err != nil {
		return fmt.Errorf("intent: vault file: %w", err)
	}
	if f.Tenant != v.tenant {
		return fmt.Errorf("intent: vault file belongs to tenant %q, not %q", f.Tenant, v.tenant)
	}
	rules := make(map[string]*Rule, len(f.Rules))
	for i := range f.Rules {
		r := f.Rules[i]
		if err := r.validate(); err != nil {
			return err
		}
		rules[r.ID] = &r
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	v.rules, v.version = rules, f.Version
	return nil
}

type vaultFile struct {
	Tenant  string `json:"tenant"`
	Version int    `json:"version"`
	Rules   []Rule `json:"rules"`
}

// Impact says how a rule affects a set of sample actions, so an owner can see
// what changes if they revoke it. Each entry compares the verdict now with
// the verdict after revoking, using the facts the engine currently holds.
type Impact struct {
	Action Action  `json:"action"`
	Before Verdict `json:"before"`
	After  Verdict `json:"after"`
}

// ErrBadKey is returned for a vault key that is not 32 bytes.
var ErrBadKey = errors.New("intent: vault key must be 32 bytes")

// Seal encrypts the exported vault with AES-256-GCM. The tenant is bound in
// as additional data so a sealed vault cannot be loaded under another tenant.
func (v *Vault) Seal(key []byte) ([]byte, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	plain, err := v.Export()
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, plain, []byte(v.tenant)), nil
}

// Unseal decrypts a Seal result into the vault. A wrong key, a changed byte or
// a different tenant fails and leaves the vault as it was.
func (v *Vault) Unseal(key, sealed []byte) error {
	gcm, err := newGCM(key)
	if err != nil {
		return err
	}
	if len(sealed) < gcm.NonceSize() {
		return errors.New("intent: sealed vault is too short")
	}
	plain, err := gcm.Open(nil, sealed[:gcm.NonceSize()], sealed[gcm.NonceSize():], []byte(v.tenant))
	if err != nil {
		return errors.New("intent: cannot open vault (wrong key, wrong tenant or damaged file)")
	}
	return v.Import(plain)
}

func newGCM(key []byte) (cipher.AEAD, error) {
	if len(key) != 32 {
		return nil, ErrBadKey
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// SaveFile writes the sealed vault to path with owner-only permissions, via a
// temporary file and rename so a crash leaves either the old file or the new
// one, never half of each.
func (v *Vault) SaveFile(path string, key []byte) error {
	sealed, err := v.Seal(key)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".vault-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(sealed); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// LoadFile reads a vault written by SaveFile.
func (v *Vault) LoadFile(path string, key []byte) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return v.Unseal(key, b)
}

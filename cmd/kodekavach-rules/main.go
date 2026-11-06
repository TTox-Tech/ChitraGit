// Command kodekavach-rules creates the signing key for KodeKavach rules
// packs, and signs and checks packs. CI uses it to publish the pack; the
// chitragit binary only ever verifies.
//
//	go run ./cmd/kodekavach-rules keygen
//	KODEKAVACH_RULES_SIGNING_KEY=... go run ./cmd/kodekavach-rules sign internal/audit/rules/pack.json dist/
//	go run ./cmd/kodekavach-rules verify <public key> dist/
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"

	"github.com/TTox-Tech/chitragit/internal/audit"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "kodekavach-rules:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return usage()
	}
	switch args[0] {
	case "keygen":
		pub, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return err
		}
		fmt.Println("Public key (repository variable KODEKAVACH_RULES_PUBLIC_KEY):")
		fmt.Println(base64.StdEncoding.EncodeToString(pub))
		fmt.Println()
		fmt.Println("Private key (repository secret KODEKAVACH_RULES_SIGNING_KEY; keep it nowhere else):")
		fmt.Println(base64.StdEncoding.EncodeToString(priv.Seed()))
		return nil
	case "sign":
		if len(args) != 3 {
			return usage()
		}
		key := os.Getenv("KODEKAVACH_RULES_SIGNING_KEY")
		if key == "" {
			return fmt.Errorf("KODEKAVACH_RULES_SIGNING_KEY is not set")
		}
		pack, err := os.ReadFile(args[1])
		if err != nil {
			return err
		}
		p, err := audit.ParseRules(pack)
		if err != nil {
			return err
		}
		sig, err := audit.SignRules(key, pack)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(args[2], 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(args[2], audit.RulesAsset), pack, 0o644); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(args[2], audit.RulesSigAsset), []byte(sig+"\n"), 0o644); err != nil {
			return err
		}
		fmt.Printf("signed rules pack %s\n", p.Version)
		return nil
	case "verify":
		if len(args) != 3 {
			return usage()
		}
		audit.RulesPublicKey = args[1]
		pack, err := os.ReadFile(filepath.Join(args[2], audit.RulesAsset))
		if err != nil {
			return err
		}
		sig, err := os.ReadFile(filepath.Join(args[2], audit.RulesSigAsset))
		if err != nil {
			return err
		}
		if err := audit.VerifyRules(pack, sig); err != nil {
			return err
		}
		p, err := audit.ParseRules(pack)
		if err != nil {
			return err
		}
		fmt.Printf("rules pack %s: signature OK\n", p.Version)
		return nil
	}
	return usage()
}

func usage() error {
	return fmt.Errorf("usage: kodekavach-rules keygen | sign <pack.json> <out-dir> | verify <public-key> <dir>")
}

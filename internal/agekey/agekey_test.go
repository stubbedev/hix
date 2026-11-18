package agekey

import (
	"strings"
	"testing"
)

var sshToAgeVectors = []struct{ ssh, age string }{
	{"ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIMpnPU/pYuebNoM5q8NzBAD5uix+12UHsJJttjEFwA0e test1", "age1q7qg72qkg7yhud8nf9g5tfc5ryg56cvwjdvyga48y4hgh5te79kqmrcwtg"},
	{"ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIC7ycmivaniHa3NWwO95S0x8wesyZkqr+qnSzz9HIzQR test2", "age122lma2me7vg89ua0j6qz00vxfp79ej3flq5phr0vxa7m98mdkveqm6m4mk"},
	{"ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAINiqqYAhVZoqa4fwuLwmzCau7prGvPBvI/8v4ZO7VFee test3", "age1engj7a8qsxwq75m4qze2jr29jkeantv9kcee0s9zlfaph2jlkvwqyxer8v"},
}

func TestFromSSHPublicKeyMatchesSSHToAge(t *testing.T) {
	for _, v := range sshToAgeVectors {
		got, err := FromSSHPublicKey([]byte(v.ssh + "\n"))
		if err != nil || string(got) != v.age {
			t.Errorf("FromSSHPublicKey(%q) = %q, %v; want %q", v.ssh, got, err, v.age)
		}
	}
}

func TestFromSSHPublicKeyRejectsOtherKeys(t *testing.T) {
	for _, key := range []string{
		"",
		"ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABAQC test",
		"ssh-ed25519 not-base64!",
		"ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIMpnPU",
	} {
		if r, err := FromSSHPublicKey([]byte(key)); err == nil {
			t.Errorf("FromSSHPublicKey(%q) = %q, want an error", key, r)
		}
	}
}

func TestParseRecipient(t *testing.T) {
	for _, v := range sshToAgeVectors {
		if _, err := ParseRecipient(v.age); err != nil {
			t.Errorf("ParseRecipient(%q): %v", v.age, err)
		}
		if _, err := ParseRecipient(strings.ToUpper(v.age)); err != nil {
			t.Errorf("ParseRecipient(upper %q): %v", v.age, err)
		}
	}
	valid := sshToAgeVectors[0].age
	for _, bad := range []string{
		"",
		"age1",
		valid[:len(valid)-1] + "q",
		"AGE1" + valid[4:],
		"bc1qw508d6qejxtdg4y5r3zarvary0c5xw7kv8f3t4",
		shortKey(t),
	} {
		if _, err := ParseRecipient(bad); err == nil {
			t.Errorf("ParseRecipient(%q) accepted", bad)
		}
	}
}

func shortKey(t *testing.T) string {
	t.Helper()
	data, err := convertBits(make([]byte, keySize/2), 8, 5, true)
	if err != nil {
		t.Fatal(err)
	}
	return bech32Encode(hrp, data)
}

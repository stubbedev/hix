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

const postQuantum = "age1pq1f6t2z97exxrwx3gzwu5q53q7fcx2r9uxxcnu5crwx9mfpwmec3u57al6fmrpqlatlzj9rxtmu2jr0qly242d50a57e44jgcgxsers038nrhhfz7m7g52fgz7fx3zzephc8qd2uhwwf3gfya6xutfljdev7njqueyx7u8nsth8feh0ayqgd40q8a6ky69z5e6qq7n49y4hmqjw3h9k93z30prhz65dd2jnggnglty7s2lkegfnvmgkqnqj723ds9tjpqq6zj9mfvflvdmtv0endafcz56tyr485yq7cg3r5vkwpz6uc7qjd9xw6ygx7r8v3uzn9v04sxc5ge942ey6c9rqlcmcyk8wd20ewsa8gqg72ws2gkz22a87ke7fyp2ml2t66tgthlf4gefvupsaccq2vqsxh2q4skpgyjjk4x5wy97favjskvfvdvudtgy67fh8p2qxvcfqmwmx689cq34w5q36zayp8seeavkfsw8e032qx2nlmq8m6jt4l8n258ed8wvfst0rm93e2m44nf8wqf3wt93ew7644zxn0tt36pc3aknrqt86hpdhhrmhggxut3k2du2jst6d89vgq6xtcptu05kw9cxenl5wsdtvkf46nj8tgc3p87crjcezlq4qwpym5fxk2zkrr9kr96t5g4ceupx87kvjz69s8m2v27whwas63rrk7n5d4hsxjg8hsj6u45mf4p8v44erkmtds8ucv3mtxz3c3caghvghgxn9ratsyhmr38nvuyrm03htfsznhz0cxlcdy2nxgrxlkafsa4rd7zuy87fwpsyyqpw8jtnxzkfz42rnvyhwcrt3gm26vk2dza5x2e2qzx2rz4th4f9jg45ztk3h84mc27agmrm7z29cqs62575e3l22zz4ykde2jq89vyknuc5gxl5ctlzm8q0vda6vzvp6hv6ynenxs4khtqyagyp2grev7c3w7uaw6rtv96fr6yketzj3cvz93rxke0nyzcs3p5tw7ajnpxev2mdxqh43qass9t7epsg9nt5q4e62pv35yrfz3psnx99rjqnfhvye2lnx2pxud6ezj723yv6zyud4r6zxjdsyeky7046uypgjyeus9svnfyv7c9arp46vffxvmfq030g9yyxuwt22zjmxwrexss4zx76ww2myh9l0uypx8ahn3qdvnzg7dpj3jm5r0yrc0yrc9u6hggpcwsxkqnpq6jv3guew352h3k83s7alvsx36fspjjeg9k9zg4swd9ls9qwj3f249zc0xt5xj9gkhxaw5ezzs4epyuudzmr5a8fcxj2ary7wsfmd8359fv4y7lxsegw6txfrnwv609rptecxwg6fpp434sy50xydtgcfhv599znwdgazcq5a22cmzjggar43gu6xzejyxmsu2xy3ul4fj8q9jqaxdrj7mp5kz3s9dc5ehkl3tg2fqa7jzvga49jf6y6ftz8w28paf3a8pelt2nzdgchp0e8kej9vgklngnjaycsnym42mf8syzsuhphrj6emt3c72czrmsvwd9m265x3ej439emrsej0hfn2hf0vdpygwsve8eksllmwqe95drzpxhvz59nchpjg87854fqwpvxtf9lgs5mesvyek6h35am5925z5dp06z9wpq4wfkfcmwurp0kpzl8ca58p0ds5xs2qdulqdr82c8vp8rtnxjuv4fq06egvkn0x70lmkq7xum6js7gfukf4dsqnxs65fue8xfe76zvvma9ydgxfvfut4ww7y2r2js2rkegvkk44pvqj7fvfgys84qc3t80g9x4r9m8tjay5emg7ngrc5eg90wgu7gvy2dkxu5kpmqkghlg77h6s5q0d04lhdlkxw25vsgq2e6f9p3e592lv07z7jel2gr6ywtuf893f4g3u6cgxdzen955ksh07npuva4qgs8q2whq"

func TestParseRecipientPostQuantum(t *testing.T) {
	for _, in := range []string{postQuantum, strings.ToUpper(postQuantum)} {
		if got, err := ParseRecipient(in); err != nil || string(got) != postQuantum {
			t.Errorf("ParseRecipient(%.20q…) = %.20q…, %v", in, got, err)
		}
	}
	short, err := convertBits(make([]byte, keySize), 8, 5, true)
	if err != nil {
		t.Fatal(err)
	}
	corrupt := postQuantum[:len(postQuantum)-1] + map[bool]string{true: "p", false: "q"}[strings.HasSuffix(postQuantum, "q")]
	for _, bad := range []string{corrupt, bech32Encode(pqHRP, short)} {
		if _, err := ParseRecipient(bad); err == nil {
			t.Errorf("ParseRecipient(%.20q…) accepted", bad)
		}
	}
}

func TestParseRecipientSSH(t *testing.T) {
	for _, v := range sshToAgeVectors {
		fields := strings.Fields(v.ssh)
		want := fields[0] + " " + fields[1]
		for _, in := range []string{v.ssh, want, v.ssh + " with spaces"} {
			if got, err := ParseRecipient(in); err != nil || string(got) != want {
				t.Errorf("ParseRecipient(%q) = %q, %v; want %q", in, got, err, want)
			}
		}
	}
	for _, bad := range []string{
		"ssh-ed25519",
		"ssh-ed25519 ",
		"ssh-ed25519 not-base64!",
		"ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIMpnPU",
		"ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABAQC test",
	} {
		if _, err := ParseRecipient(bad); err == nil {
			t.Errorf("ParseRecipient(%q) accepted", bad)
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

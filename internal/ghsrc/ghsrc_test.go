package ghsrc

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

func TestParseURL(t *testing.T) {
	ok := []struct {
		in, fetch, slug string
		private         bool
	}{
		{"https://github.com/acme/site", "https://github.com/acme/site.git", "acme/site", false},
		{"https://github.com/acme/site.git", "https://github.com/acme/site.git", "acme/site", false},
		{" https://github.com/acme/site/ ", "https://github.com/acme/site.git", "acme/site", false},
		{"git@github.com:acme/site.git", "ssh://git@github.com/acme/site.git", "acme/site", true},
		{"ssh://git@github.com/acme/my.repo_1", "ssh://git@github.com/acme/my.repo_1.git", "acme/my.repo_1", true},
	}
	for _, c := range ok {
		f, p, s, err := ParseURL(c.in)
		if err != nil || f != c.fetch || s != c.slug || p != c.private {
			t.Errorf("%q → %q %v %q %v", c.in, f, p, s, err)
		}
	}
	for _, bad := range []string{
		"", "file:///etc/passwd", "/tmp/repo", "ext::sh -c id", "--upload-pack=x", "http://github.com/a/b", "https://evil.com/a/b",
		"https://github.com/a", "https://github.com/a/b/c", "https://github.com/-a/b", "https://github.com/a/..",
		"https://github.com/a/b?x=1", "https://github.com/a/b c", "git@evil.com:a/b.git", "https://github.com.evil.com/a/b",
		"https://github.com/a/--b",
	} {
		if _, _, _, err := ParseURL(bad); err == nil {
			t.Errorf("deveria recusar %q", bad)
		}
	}
}

func TestValidBranch(t *testing.T) {
	for _, b := range []string{"main", "release/1.0", "feat_x-1", "v1.2.3"} {
		if !ValidBranch(b) {
			t.Errorf("%q deveria valer", b)
		}
	}
	for _, b := range []string{"", "-x", "--force", "a..b", "a//b", "a/", "x.lock", "a b", "a:b", "a~1", "a^", "/a", ".hidden"} {
		if ValidBranch(b) {
			t.Errorf("%q deveria ser recusado", b)
		}
	}
}

func TestVerifySignature(t *testing.T) {
	secret, body := []byte("s3gredo"), []byte(`{"ref":"x"}`)
	m := hmac.New(sha256.New, secret)
	m.Write(body)
	good := "sha256=" + hex.EncodeToString(m.Sum(nil))
	if !VerifySignature(secret, body, good) {
		t.Fatal("assinatura correta recusada")
	}
	for _, bad := range []string{"", "sha256=", "sha256=zz", good[7:], strings.Replace(good, "sha256=", "sha1=", 1), good + "00"} {
		if VerifySignature(secret, body, bad) {
			t.Errorf("aceitou %q", bad)
		}
	}
	if VerifySignature([]byte("outro"), body, good) || VerifySignature(secret, []byte("alterado"), good) {
		t.Fatal("aceitou segredo ou corpo errado")
	}
}

func TestDeployKeyIsUsable(t *testing.T) {
	priv, pub, err := newDeployKey("droidpaas-blog")
	if err != nil || !strings.HasPrefix(string(priv), "-----BEGIN OPENSSH PRIVATE KEY-----") || !strings.HasPrefix(pub, "ssh-ed25519 ") || !strings.HasSuffix(pub, " droidpaas-blog") {
		t.Fatalf("%v\n%s\n%s", err, priv, pub)
	}
}

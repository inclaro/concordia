package digest_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/inclaro/concordia/internal/digest"
)

const emptyHex = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

func TestSHA256OfEmptyInput(t *testing.T) {
	t.Parallel()

	if got := digest.SHA256Hex(nil); got != emptyHex {
		t.Errorf("got %s", got)
	}

	if got := digest.SHA256HexString(""); got != emptyHex {
		t.Errorf("got %s", got)
	}

	if got := digest.OfBytes([]byte{}); got != "sha256:"+emptyHex {
		t.Errorf("got %s", got)
	}
}

func TestOfFile(t *testing.T) {
	t.Parallel()

	p := filepath.Join(t.TempDir(), "f.txt")
	if err := os.WriteFile(p, []byte("abc"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := digest.OfFile(p)
	if err != nil {
		t.Fatal(err)
	}

	if got != "sha256:ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad" {
		t.Errorf("got %s", got)
	}

	if _, err := digest.OfFile(p + ".missing"); err == nil {
		t.Error("expected an error")
	}
}

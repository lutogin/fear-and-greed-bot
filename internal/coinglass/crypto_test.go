package coinglass

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"crypto/aes"
	"encoding/base64"
	"strings"
	"testing"
)

// encrypt is the inverse of decrypt, used to build test responses.
func encrypt(t *testing.T, plain, key []byte, useGzip bool) string {
	t.Helper()
	var buf bytes.Buffer
	var w interface {
		Write([]byte) (int, error)
		Close() error
	}
	if useGzip {
		w = gzip.NewWriter(&buf)
	} else {
		w = zlib.NewWriter(&buf)
	}
	if _, err := w.Write(plain); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	data := buf.Bytes()
	pad := aes.BlockSize - len(data)%aes.BlockSize
	data = append(data, bytes.Repeat([]byte{byte(pad)}, pad)...)

	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]byte, len(data))
	for i := 0; i < len(data); i += aes.BlockSize {
		block.Encrypt(out[i:i+aes.BlockSize], data[i:i+aes.BlockSize])
	}
	return base64.StdEncoding.EncodeToString(out)
}

// A real "user" header returned by capi.coinglass.com together with v=66: it
// pins the key derivation and the encoding to what the site actually does.
func TestDecryptRealUserHeader(t *testing.T) {
	key, err := sessionKey("66", "", "", historyPath)
	if err != nil {
		t.Fatal(err)
	}
	got, err := decrypt("7LY2NUhq3GH/M2AY9j0Y3ZbPRM7sP8fp/ml3f+gXxlFuY92dGJdkB/gz7nx81q1h", key)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "15f0ed877f4c461f" {
		t.Errorf("decrypted data key = %q, want %q", got, "15f0ed877f4c461f")
	}
}

func TestSessionKey(t *testing.T) {
	tests := []struct {
		version string
		want    string
	}{
		{"0", "MTc5MDI3MjQ3MTY1"}, // base64 of the cache-ts-v2 request header
		{"1", "L2FwaS9pbmRleC9o"}, // base64 of the request path
		{"2", "MTcwMDAwMDAwMDAw"}, // base64 of the time response header
		{"55", "MTcwYjA3MGRhOTY1"},
		{"66", "ZDY1MzdkODQ1YTk2"},
		{"77", "ODYzZjA4Njg5Yzk3"},
	}
	for _, tt := range tests {
		got, err := sessionKey(tt.version, "1790272471655", "1700000000000", historyPath)
		if err != nil {
			t.Errorf("v=%s: %v", tt.version, err)
			continue
		}
		if string(got) != tt.want {
			t.Errorf("v=%s: key = %q, want %q", tt.version, got, tt.want)
		}
	}
}

func TestSessionKeyErrors(t *testing.T) {
	if _, err := sessionKey("99", "1790272471655", "", historyPath); err == nil || !strings.Contains(err.Error(), "unsupported encryption version") {
		t.Errorf("unknown version: err = %v", err)
	}
	if _, err := sessionKey("2", "1790272471655", "", historyPath); err == nil {
		t.Error("v=2 without the time header must fail")
	}
}

func TestDecryptRoundTrip(t *testing.T) {
	key := []byte("0123456789abcdef")
	for _, tt := range []struct {
		name  string
		plain string
		gzip  bool
		want  string
	}{
		{name: "zlib", plain: `[{"values":[1]}]`, want: `[{"values":[1]}]`},
		{name: "gzip", plain: `[{"values":[1]}]`, gzip: true, want: `[{"values":[1]}]`},
		{name: "quoted string", plain: `"15f0ed877f4c461f"`, want: "15f0ed877f4c461f"},
		{name: "block-aligned", plain: strings.Repeat("x", 64), want: strings.Repeat("x", 64)},
		{name: "unicode", plain: `"страх"`, want: "страх"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := decrypt(encrypt(t, []byte(tt.plain), key, tt.gzip), key)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDecryptErrors(t *testing.T) {
	key := []byte("0123456789abcdef")
	valid := encrypt(t, []byte(`[1]`), key, false)
	notCompressed := func() string {
		// Correctly padded and encrypted, but not zlib data.
		block, _ := aes.NewCipher(key)
		data := append([]byte("plain text!!"), 4, 4, 4, 4)
		out := make([]byte, len(data))
		block.Encrypt(out, data)
		return base64.StdEncoding.EncodeToString(out)
	}()
	tests := []struct {
		name string
		in   string
		key  []byte
	}{
		{name: "not base64", in: "%%%", key: key},
		{name: "empty", in: "", key: key},
		{name: "not a whole block", in: base64.StdEncoding.EncodeToString([]byte("short")), key: key},
		{name: "invalid key size", in: valid, key: []byte("short")},
		{name: "wrong key", in: valid, key: []byte("fedcba9876543210")},
		{name: "not compressed", in: notCompressed, key: key},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got, err := decrypt(tt.in, tt.key); err == nil {
				t.Errorf("expected an error, got %q", got)
			}
		})
	}
}

func TestPKCS7Unpad(t *testing.T) {
	block := func(tail ...byte) []byte {
		return append(bytes.Repeat([]byte{'a'}, aes.BlockSize-len(tail)), tail...)
	}
	if got, err := pkcs7Unpad(block(3, 3, 3)); err != nil || len(got) != aes.BlockSize-3 {
		t.Errorf("valid padding: got %d bytes, err %v", len(got), err)
	}
	for name, in := range map[string][]byte{
		"zero":         block(0),
		"too large":    block(17),
		"inconsistent": block(1, 2, 3, 3),
	} {
		if _, err := pkcs7Unpad(in); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

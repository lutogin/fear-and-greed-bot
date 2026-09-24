package coinglass

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"crypto/aes"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
)

// maxDecoded caps the decompressed payload (the real one is ~0.5 MB).
const maxDecoded = 32 << 20

// staticSeeds are the key seeds the site's JS uses for these values of the "v"
// response header.
var staticSeeds = map[string]string{
	"55": "170b070da9654622",
	"66": "d6537d845a964081",
	"77": "863f08689c97435b",
}

// sessionKey derives the key that decrypts the "user" response header, the way the
// site's JS does: pick a seed by the "v" response header, base64 it, keep 16 chars.
func sessionKey(version, requestTS, responseTime, path string) ([]byte, error) {
	var seed string
	switch version {
	case "0":
		seed = requestTS // the "cache-ts-v2" request header
	case "1":
		seed = path
	case "2":
		seed = responseTime // the "time" response header
	default:
		s, ok := staticSeeds[version]
		if !ok {
			return nil, fmt.Errorf("unsupported encryption version v=%q (coinglass.com changed its page, the bot needs an update)", version)
		}
		seed = s
	}
	encoded := base64.StdEncoding.EncodeToString([]byte(seed))
	if len(encoded) < 16 {
		return nil, fmt.Errorf("key seed %q for v=%q is too short", seed, version)
	}
	return []byte(encoded[:16]), nil
}

// decrypt reverses the encoding of the CoinGlass web API:
// base64 → AES-ECB with PKCS#7 padding → zlib or gzip → text, optionally in quotes.
func decrypt(b64 string, key []byte) ([]byte, error) {
	ciphertext, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil, fmt.Errorf("base64: %w", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	if len(ciphertext) == 0 || len(ciphertext)%aes.BlockSize != 0 {
		return nil, fmt.Errorf("ciphertext length %d is not a multiple of the AES block size", len(ciphertext))
	}
	plain := make([]byte, len(ciphertext))
	for i := 0; i < len(ciphertext); i += aes.BlockSize {
		block.Decrypt(plain[i:i+aes.BlockSize], ciphertext[i:i+aes.BlockSize])
	}
	plain, err = pkcs7Unpad(plain)
	if err != nil {
		return nil, err
	}
	out, err := inflate(plain)
	if err != nil {
		return nil, err
	}
	out = bytes.TrimPrefix(out, []byte(`"`))
	return bytes.TrimSuffix(out, []byte(`"`)), nil
}

func pkcs7Unpad(b []byte) ([]byte, error) {
	n := len(b)
	if n == 0 {
		return nil, errors.New("empty plaintext")
	}
	pad := int(b[n-1])
	if pad == 0 || pad > aes.BlockSize || pad > n {
		return nil, errors.New("invalid padding (wrong key?)")
	}
	for _, c := range b[n-pad:] {
		if int(c) != pad {
			return nil, errors.New("invalid padding (wrong key?)")
		}
	}
	return b[:n-pad], nil
}

// inflate decompresses zlib or gzip data, like pako.inflate does in the browser.
func inflate(b []byte) ([]byte, error) {
	var (
		r   io.ReadCloser
		err error
	)
	if len(b) >= 2 && b[0] == 0x1f && b[1] == 0x8b {
		r, err = gzip.NewReader(bytes.NewReader(b))
	} else {
		r, err = zlib.NewReader(bytes.NewReader(b))
	}
	if err != nil {
		return nil, fmt.Errorf("decompress: %w", err)
	}
	defer r.Close()
	out, err := io.ReadAll(io.LimitReader(r, maxDecoded+1))
	if err != nil {
		return nil, fmt.Errorf("decompress: %w", err)
	}
	if len(out) > maxDecoded {
		return nil, fmt.Errorf("decompressed payload exceeds %d bytes", maxDecoded)
	}
	return out, nil
}

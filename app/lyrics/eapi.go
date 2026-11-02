package lyrics

import (
	"crypto/aes"
	"crypto/md5"
	"encoding/hex"
	"strings"
)

const (
	// eapiKey is the key NetEase's eapi body is AES-128-ECB encrypted with.
	eapiKey = "e82ckenh8dichen8"
	// eapiSeparator is the literal between the URL, the JSON, and the digest.
	eapiSeparator = "-36cd479b6b5-"
)

// eapiPrefixes are the interface hosts whose "/e" prefix is stripped before
// the digest is built.
var eapiPrefixes = []string{
	"https://interface3.music.163.com/e",
	"https://interface.music.163.com/e",
}

// encryptEAPI returns the eapi request's params: the URL, the JSON body, and
// their MD5 digest, AES-128-ECB encrypted and written as upper-case hex.
func encryptEAPI(rawURL, data string) (string, error) {
	inner := rawURL
	for _, prefix := range eapiPrefixes {
		inner = strings.ReplaceAll(inner, prefix, "/")
	}
	digest := md5.Sum([]byte("nobody" + inner + "use" + data + "md5forencrypt"))
	plain := inner + eapiSeparator + data + eapiSeparator + hex.EncodeToString(digest[:])
	padded := pkcs7Pad([]byte(plain), aes.BlockSize)
	block, err := aes.NewCipher([]byte(eapiKey))
	if err != nil {
		return "", err
	}
	out := make([]byte, len(padded))
	for i := 0; i < len(padded); i += aes.BlockSize {
		block.Encrypt(out[i:i+aes.BlockSize], padded[i:i+aes.BlockSize])
	}
	return strings.ToUpper(hex.EncodeToString(out)), nil
}

// pkcs7Pad pads data to a whole number of blocks. Eapi always adds padding,
// even when the data already ends on a block boundary.
func pkcs7Pad(data []byte, blockSize int) []byte {
	padding := blockSize - len(data)%blockSize
	out := make([]byte, len(data)+padding)
	copy(out, data)
	for i := len(data); i < len(out); i++ {
		out[i] = byte(padding)
	}
	return out
}

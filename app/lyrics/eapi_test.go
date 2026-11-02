package lyrics

import "testing"

// TestEncryptEAPIMatchesTheReference checks the URL prefix stripping, the
// MD5 digest, the PKCS7 padding and the upper-case hex against the value the
// upstream C# client produced for the same URL and JSON.
func TestEncryptEAPIMatchesTheReference(t *testing.T) {
	const rawURL = "https://interface3.music.163.com/eapi/song/lyric/v1"
	const data = `{"cp":"false","csrf_token":"","id":"423997333","kv":"0","lv":"0","rv":"0","tv":"0","yrv":"0","ytv":"0","yv":"0"}`
	const want = "04AE33D34A93FE3EC22DA8FA305D290AB337D0FE5F36D211DE0D338CC6AA89D0" +
		"35DF0DF6670E9141B8FEB05134B45228856D603755651D56F4AD80DF0AE37F4E" +
		"325FEC7848021CACAC97B0297AA242CA41BB225BD74C9B97F4F2BA482D108F70F" +
		"173EDB093AC1C5B0B390F4ADA3666786894C034F63137522CE605164CFBBA7B8D" +
		"40BA661477B9A5EC6EA773131F4474A7422BFFFC535166212FC3FACA86691B937C" +
		"DDA03BF9BE46BFA91A2DE2847A3DB90146F2B8340B1A7AAF3925799EEA4C"
	got, err := encryptEAPI(rawURL, data)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("params = %s;\nwant %s", got, want)
	}
}

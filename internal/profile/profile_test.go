package profile

import (
	"bytes"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"net"
	"net/url"
	"strings"
	"testing"
)

func TestValidateURLRejectsSSRFTargets(t *testing.T) {
	for _, raw := range []string{"http://localhost/image.png", "https://127.0.0.1/image.png", "https://metadata.google.internal/image.png"} {
		parsed, _ := url.Parse(raw)
		if err := ValidateURL(parsed, map[string]struct{}{"example.com": {}}); err == nil {
			t.Fatalf("accepted SSRF target %s", raw)
		}
	}
}

func TestProfileTextLimits(t *testing.T) {
	long := strings.Repeat("x", 33)
	if err := validateMemberUpdate(MemberUpdate{Nickname: &long}); err == nil {
		t.Fatal("accepted an oversized nickname")
	}
	long = strings.Repeat("x", 191)
	if err := validateMemberUpdate(MemberUpdate{Bio: &long}); err == nil {
		t.Fatal("accepted an oversized bio")
	}
}

func TestValidateURLRequiresExplicitAllowlist(t *testing.T) {
	parsed, _ := url.Parse("https://example.com/image.png")
	if err := ValidateURL(parsed, map[string]struct{}{}); err == nil {
		t.Fatal("accepted an empty allowlist")
	}
	if err := ValidateURL(parsed, map[string]struct{}{"example.com": {}}); err != nil {
		t.Fatal(err)
	}
}

func TestDecodeAssetDetectsRealImageFormat(t *testing.T) {
	imageData := image.NewRGBA(image.Rect(0, 0, 2, 2))
	imageData.Set(0, 0, color.RGBA{R: 255, A: 255})

	cases := []struct {
		name  string
		mime  string
		write func(*bytes.Buffer) error
	}{
		{name: "png", mime: "image/png", write: func(buffer *bytes.Buffer) error { return png.Encode(buffer, imageData) }},
		{name: "jpeg", mime: "image/jpeg", write: func(buffer *bytes.Buffer) error { return jpeg.Encode(buffer, imageData, nil) }},
		{name: "gif", mime: "image/gif", write: func(buffer *bytes.Buffer) error { return gif.Encode(buffer, imageData, nil) }},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			var buffer bytes.Buffer
			if err := test.write(&buffer); err != nil {
				t.Fatal(err)
			}
			asset, err := decodeAsset("https://cdn.discordapp.com/file", buffer.Bytes(), 100)
			if err != nil {
				t.Fatal(err)
			}
			if asset.MIME != test.mime {
				t.Fatalf("mime=%q want %q", asset.MIME, test.mime)
			}
			if !strings.HasPrefix(asset.DataURI, "data:"+test.mime+";base64,") {
				t.Fatalf("wrong data URI prefix: %q", asset.DataURI)
			}
		})
	}
}

func TestDecodeAssetRejectsUnsupportedBytes(t *testing.T) {
	if _, err := decodeAsset("https://cdn.discordapp.com/file", []byte("not an image"), 100); err == nil || !strings.Contains(err.Error(), "PNG, JPEG, or GIF") {
		t.Fatalf("expected clear unsupported-format error, got %v", err)
	}
}

func TestPrivateIPClassification(t *testing.T) {
	for _, raw := range []string{"127.0.0.1", "10.0.0.1", "100.64.0.1", "169.254.169.254", "192.0.2.1", "198.18.0.1", "203.0.113.1", "::1", "fc00::1", "fe80::1", "2001:db8::1"} {
		if !isPrivateIP(net.ParseIP(raw)) {
			t.Fatalf("non-public address %s was accepted", raw)
		}
	}
	if isPrivateIP(net.ParseIP("8.8.8.8")) {
		t.Fatal("public unicast address was classified as private")
	}
}

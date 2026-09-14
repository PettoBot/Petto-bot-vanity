package profile

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/PettoBot/vanity-tag-bot/internal/database"
)

type Asset struct {
	Reference string
	DataURI   string
	MIME      string
	Width     int
	Height    int
	Size      int64
}

type Fetcher struct {
	Client       *http.Client
	MaxBytes     int64
	MaxPixels    int64
	AllowedHosts map[string]struct{}
}

func NewFetcher(maxBytes, maxPixels int64, allowedHosts map[string]struct{}) *Fetcher {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		ips, err := net.DefaultResolver.LookupIP(ctx, "ip", host)
		if err != nil {
			return nil, err
		}
		for _, ip := range ips {
			if isPrivateIP(ip) {
				continue
			}
			dialer := net.Dialer{Timeout: 10 * time.Second}
			return dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		}
		return nil, fmt.Errorf("asset host resolves only to private or local addresses")
	}
	return &Fetcher{
		Client: &http.Client{
			Transport: transport,
			Timeout:   10 * time.Second,
			CheckRedirect: func(req *http.Request, _ []*http.Request) error {
				return ValidateURL(req.URL, allowedHosts)
			},
		},
		MaxBytes: maxBytes, MaxPixels: maxPixels, AllowedHosts: allowedHosts,
	}
}

func (f *Fetcher) Fetch(ctx context.Context, rawURL string) (Asset, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return Asset{}, fmt.Errorf("invalid asset URL: %w", err)
	}
	if err := ValidateURL(parsed, f.AllowedHosts); err != nil {
		return Asset{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return Asset{}, err
	}
	response, err := f.Client.Do(req)
	if err != nil {
		return Asset{}, fmt.Errorf("download profile asset: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return Asset{}, fmt.Errorf("profile asset returned HTTP %d", response.StatusCode)
	}
	if response.ContentLength > f.MaxBytes {
		return Asset{}, fmt.Errorf("profile asset exceeds %d bytes", f.MaxBytes)
	}
	mime := strings.ToLower(strings.TrimSpace(strings.Split(response.Header.Get("Content-Type"), ";")[0]))
	if !allowedMIME(mime) {
		return Asset{}, fmt.Errorf("unsupported profile asset MIME %q", mime)
	}
	limited := io.LimitReader(response.Body, f.MaxBytes+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return Asset{}, fmt.Errorf("read profile asset: %w", err)
	}
	if int64(len(data)) > f.MaxBytes {
		return Asset{}, fmt.Errorf("profile asset exceeds %d bytes", f.MaxBytes)
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return Asset{}, fmt.Errorf("decode profile asset: %w", err)
	}
	if int64(config.Width)*int64(config.Height) > f.MaxPixels {
		return Asset{}, fmt.Errorf("profile asset exceeds %d pixels", f.MaxPixels)
	}
	if mime == "" || mime == "application/octet-stream" {
		mime = formatMIME(format)
	}
	return Asset{Reference: parsed.String(), DataURI: "data:" + mime + ";base64," + encodeBase64(data), MIME: mime,
		Width: config.Width, Height: config.Height, Size: int64(len(data))}, nil
}

func ValidateURL(parsed *url.URL, allowedHosts map[string]struct{}) error {
	if parsed == nil || parsed.Scheme != "https" || parsed.Hostname() == "" {
		return fmt.Errorf("profile assets require an HTTPS URL")
	}
	host := strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
	// Discord command attachments are served from these public CDN hosts. They
	// are safe to accept without forcing every deployment to hard-code them in
	// ASSET_ALLOWED_HOSTS. All other external URLs still require an explicit
	// allowlist entry.
	discordCDN := host == "cdn.discordapp.com" || host == "media.discordapp.net"
	if len(allowedHosts) == 0 && !discordCDN {
		return fmt.Errorf("asset host allowlist is empty")
	}
	if _, ok := allowedHosts[host]; !ok && !discordCDN {
		return fmt.Errorf("asset host %q is not allowlisted", host)
	}
	if host == "localhost" || strings.HasSuffix(host, ".local") || host == "metadata.google.internal" {
		return fmt.Errorf("private or metadata hosts are not allowed")
	}
	if ip := net.ParseIP(host); ip != nil {
		return fmt.Errorf("literal IP asset URLs are not allowed")
	}
	lookupCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	addresses, err := net.DefaultResolver.LookupIPAddr(lookupCtx, host)
	if err != nil {
		return fmt.Errorf("resolve asset host: %w", err)
	}
	for _, address := range addresses {
		if isPrivateIP(address.IP) {
			return fmt.Errorf("asset host resolves to a private or local address")
		}
	}
	return nil
}

type API interface {
	ModifyCurrentMember(context.Context, string, MemberUpdate) error
}

type MemberUpdate struct {
	Nickname *string
	Avatar   *string
	Banner   *string
	Bio      *string
}

type Service struct {
	Store   *database.Store
	API     API
	Fetcher *Fetcher
}

func (s *Service) Update(ctx context.Context, guildID string, update MemberUpdate, refs database.BotProfile) error {
	if err := validateMemberUpdate(update); err != nil {
		return err
	}
	if err := s.API.ModifyCurrentMember(ctx, guildID, update); err != nil {
		previous, loadErr := s.Store.GetBotProfile(ctx, guildID)
		if loadErr == nil {
			previous.SyncStatus = "error"
			previous.LastError = err.Error()
			_ = s.Store.SaveBotProfile(ctx, previous)
		}
		return err
	}
	refs.GuildID = guildID
	refs.SyncStatus = "synced"
	refs.LastError = ""
	return s.Store.SaveBotProfile(ctx, refs)
}

func validateMemberUpdate(update MemberUpdate) error {
	if update.Nickname != nil && utf8.RuneCountInString(*update.Nickname) > 32 {
		return fmt.Errorf("nickname exceeds Discord's 32-character limit")
	}
	if update.Bio != nil && utf8.RuneCountInString(*update.Bio) > 190 {
		return fmt.Errorf("bio exceeds the configured 190-character limit")
	}
	return nil
}

func (s *Service) UpdateAsset(ctx context.Context, guildID, kind, rawURL string, current database.BotProfile) error {
	asset, err := s.Fetcher.Fetch(ctx, rawURL)
	if err != nil {
		return err
	}
	update := MemberUpdate{}
	if kind == "avatar" {
		update.Avatar = &asset.DataURI
		current.AvatarRef = &asset.Reference
	} else if kind == "banner" {
		update.Banner = &asset.DataURI
		current.BannerRef = &asset.Reference
	} else {
		return fmt.Errorf("unsupported profile asset kind %q", kind)
	}
	return s.Update(ctx, guildID, update, current)
}

func isPrivateIP(ip net.IP) bool {
	privateRanges := []string{"10.0.0.0/8", "100.64.0.0/10", "127.0.0.0/8", "169.254.0.0/16", "172.16.0.0/12", "192.0.0.0/24", "192.168.0.0/16", "198.18.0.0/15", "::1/128", "fc00::/7", "fe80::/10"}
	for _, raw := range privateRanges {
		_, network, _ := net.ParseCIDR(raw)
		if network.Contains(ip) {
			return true
		}
	}
	return false
}

func allowedMIME(mime string) bool {
	return mime == "image/png" || mime == "image/jpeg" || mime == "image/gif"
}

func formatMIME(format string) string {
	switch strings.ToLower(format) {
	case "png":
		return "image/png"
	case "jpeg":
		return "image/jpeg"
	case "gif":
		return "image/gif"
	default:
		return "application/octet-stream"
	}
}

func encodeBase64(data []byte) string {
	return base64.StdEncoding.EncodeToString(data)
}

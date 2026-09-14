package embeds

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/bwmarrin/discordgo"
)

// Template is the JSON document stored in embed_templates.payload. The flat
// fields keep compatibility with the first version of Vanity Tag Bot, while
// UnmarshalJSON also accepts the nested author/footer shape used by Petto.
type Template struct {
	Content     string             `json:"content,omitempty"`
	Title       string             `json:"title,omitempty"`
	Description string             `json:"description,omitempty"`
	Color       int                `json:"color,omitempty"`
	URL         string             `json:"url,omitempty"`
	Thumbnail   string             `json:"thumbnail,omitempty"`
	Image       string             `json:"image,omitempty"`
	Timestamp   bool               `json:"timestamp,omitempty"`
	Footer      string             `json:"-"`
	FooterIcon  string             `json:"-"`
	Author      string             `json:"-"`
	AuthorIcon  string             `json:"-"`
	AuthorURL   string             `json:"-"`
	Fields      []TemplateField    `json:"fields,omitempty"`
	Buttons     [][]TemplateButton `json:"buttons,omitempty"`
}

type TemplateField struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Inline bool   `json:"inline"`
}

// TemplateButton is intentionally limited to link buttons. Link buttons do
// not create custom interaction state and can be safely reused in any channel.
type TemplateButton struct {
	Label    string `json:"label"`
	URL      string `json:"url"`
	Disabled bool   `json:"disabled,omitempty"`
}

// UnmarshalJSON accepts both the original flat strings and Petto's nested
// objects for author and footer. This lets existing rows keep working without
// a data migration.
func (t *Template) UnmarshalJSON(data []byte) error {
	type wire struct {
		Content     string             `json:"content"`
		Title       string             `json:"title"`
		Description string             `json:"description"`
		Color       int                `json:"color"`
		URL         string             `json:"url"`
		Thumbnail   string             `json:"thumbnail"`
		Image       string             `json:"image"`
		Timestamp   json.RawMessage    `json:"timestamp"`
		Footer      json.RawMessage    `json:"footer"`
		Author      json.RawMessage    `json:"author"`
		Fields      []TemplateField    `json:"fields"`
		Buttons     [][]TemplateButton `json:"buttons"`
	}
	var value wire
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}

	*t = Template{
		Content: value.Content, Title: value.Title, Description: value.Description,
		Color: value.Color, URL: value.URL, Thumbnail: value.Thumbnail,
		Image: value.Image, Fields: value.Fields, Buttons: value.Buttons,
	}
	if len(value.Timestamp) > 0 && string(value.Timestamp) != "null" {
		var enabled bool
		if err := json.Unmarshal(value.Timestamp, &enabled); err == nil {
			t.Timestamp = enabled
		} else {
			var timestamp string
			if err := json.Unmarshal(value.Timestamp, &timestamp); err == nil {
				t.Timestamp = timestamp != ""
			}
		}
	}

	parseAuthor(value.Author, t)
	parseFooter(value.Footer, t)
	return nil
}

func parseAuthor(raw json.RawMessage, t *Template) {
	if len(raw) == 0 || string(raw) == "null" {
		return
	}
	var legacy string
	if json.Unmarshal(raw, &legacy) == nil {
		t.Author = legacy
		return
	}
	var value struct {
		Name    string `json:"name"`
		Icon    string `json:"icon"`
		IconURL string `json:"icon_url"`
		URL     string `json:"url"`
	}
	if json.Unmarshal(raw, &value) == nil {
		t.Author, t.AuthorIcon, t.AuthorURL = value.Name, value.Icon, value.URL
		if t.AuthorIcon == "" {
			t.AuthorIcon = value.IconURL
		}
	}
}

func parseFooter(raw json.RawMessage, t *Template) {
	if len(raw) == 0 || string(raw) == "null" {
		return
	}
	var legacy string
	if json.Unmarshal(raw, &legacy) == nil {
		t.Footer = legacy
		return
	}
	var value struct {
		Text    string `json:"text"`
		Icon    string `json:"icon"`
		IconURL string `json:"icon_url"`
	}
	if json.Unmarshal(raw, &value) == nil {
		t.Footer, t.FooterIcon = value.Text, value.Icon
		if t.FooterIcon == "" {
			t.FooterIcon = value.IconURL
		}
	}
}

// MarshalJSON writes the nested shape used by Petto while keeping all old
// fields and the same database column. Existing data is only rewritten when a
// user explicitly saves a template.
func (t Template) MarshalJSON() ([]byte, error) {
	type author struct {
		Name string `json:"name"`
		Icon string `json:"icon,omitempty"`
		URL  string `json:"url,omitempty"`
	}
	type footer struct {
		Text string `json:"text"`
		Icon string `json:"icon,omitempty"`
	}
	type wire struct {
		Content     string             `json:"content,omitempty"`
		Title       string             `json:"title,omitempty"`
		Description string             `json:"description,omitempty"`
		Color       int                `json:"color,omitempty"`
		URL         string             `json:"url,omitempty"`
		Thumbnail   string             `json:"thumbnail,omitempty"`
		Image       string             `json:"image,omitempty"`
		Timestamp   bool               `json:"timestamp,omitempty"`
		Footer      *footer            `json:"footer,omitempty"`
		Author      *author            `json:"author,omitempty"`
		Fields      []TemplateField    `json:"fields,omitempty"`
		Buttons     [][]TemplateButton `json:"buttons,omitempty"`
	}
	value := wire{
		Content: t.Content, Title: t.Title, Description: t.Description, Color: t.Color,
		URL: t.URL, Thumbnail: t.Thumbnail, Image: t.Image, Timestamp: t.Timestamp,
		Fields: t.Fields, Buttons: t.Buttons,
	}
	if t.Author != "" {
		value.Author = &author{Name: t.Author, Icon: t.AuthorIcon, URL: t.AuthorURL}
	}
	if t.Footer != "" {
		value.Footer = &footer{Text: t.Footer, Icon: t.FooterIcon}
	}
	return json.Marshal(value)
}

type Variables struct {
	UserID          string
	UserName        string
	UserMention     string
	UserAvatar      string
	UserDisplayName string
	GuildID         string
	GuildName       string
	GuildIcon       string
	ChannelID       string
	ChannelName     string
	RuleName        string
	RuleSource      string
	RuleValue       string
	RuleCondition   string
	RuleReason      string
	RoleID          string
	Action          string
	ActionText      string
	Result          string
	EventName       string
	EventTitle      string
	EventError      string
	MatchedValue    string
	VanityRule      string
	VanityWord      string
	VanitySource    string
	VanityValue     string
	VanityRoleID    string
	TagRule         string
	TagCondition    string
	Tag             string
	TagRuleValue    string
	TagGuildID      string
	TagEnabled      string
	TagBadge        string
	TagRoleID       string
	Timestamp       time.Time
}

type MessagePayload struct {
	Content         string
	Embeds          []*discordgo.MessageEmbed
	Components      []discordgo.MessageComponent
	AllowedMentions *discordgo.MessageAllowedMentions
}

var variablePattern = regexp.MustCompile(`\{[a-z][a-z0-9_]*(?:\.[a-z][a-z0-9_]*)?(?::[^{}]+)?\}`)

func Render(template Template, vars Variables) (*discordgo.MessageEmbed, error) {
	result := &discordgo.MessageEmbed{
		Title:       resolveText(template.Title, vars),
		Description: resolveText(template.Description, vars),
		Color:       template.Color,
	}
	if template.URL != "" {
		value, err := resolveURL(template.URL, vars, "title URL")
		if err != nil {
			return nil, err
		}
		result.URL = value
	}
	if template.Timestamp {
		timestamp := vars.Timestamp
		if timestamp.IsZero() {
			timestamp = time.Now().UTC()
		}
		result.Timestamp = timestamp.UTC().Format(time.RFC3339)
	}
	if template.Thumbnail != "" {
		value, err := resolveURL(template.Thumbnail, vars, "thumbnail URL")
		if err != nil {
			return nil, err
		}
		if value != "" {
			result.Thumbnail = &discordgo.MessageEmbedThumbnail{URL: value}
		}
	}
	if template.Image != "" {
		value, err := resolveURL(template.Image, vars, "image URL")
		if err != nil {
			return nil, err
		}
		if value != "" {
			result.Image = &discordgo.MessageEmbedImage{URL: value}
		}
	}
	if template.Footer != "" {
		footer := &discordgo.MessageEmbedFooter{Text: resolveText(template.Footer, vars)}
		if template.FooterIcon != "" {
			value, err := resolveURL(template.FooterIcon, vars, "footer icon URL")
			if err != nil {
				return nil, err
			}
			footer.IconURL = value
		}
		result.Footer = footer
	}
	if template.Author != "" {
		author := &discordgo.MessageEmbedAuthor{Name: resolveText(template.Author, vars)}
		if template.AuthorIcon != "" {
			value, err := resolveURL(template.AuthorIcon, vars, "author icon URL")
			if err != nil {
				return nil, err
			}
			author.IconURL = value
		}
		if template.AuthorURL != "" {
			value, err := resolveURL(template.AuthorURL, vars, "author URL")
			if err != nil {
				return nil, err
			}
			author.URL = value
		}
		result.Author = author
	}
	for _, field := range template.Fields {
		result.Fields = append(result.Fields, &discordgo.MessageEmbedField{
			Name: resolveText(field.Name, vars), Value: resolveText(field.Value, vars), Inline: field.Inline,
		})
	}
	if err := Validate(result); err != nil {
		return nil, err
	}
	return result, nil
}

// Preview renders the saved shape without resolving runtime variables. URLs
// containing variables are left out of the preview because they are not valid
// until a real member/guild context exists.
func Preview(template Template) (*discordgo.MessageEmbed, error) {
	result := &discordgo.MessageEmbed{Title: template.Title, Description: template.Description, Color: template.Color}
	if template.URL != "" && !strings.Contains(template.URL, "{") {
		if parsed, err := url.Parse(template.URL); err == nil && parsed.Host != "" && (parsed.Scheme == "http" || parsed.Scheme == "https") {
			result.URL = template.URL
		}
	}
	if template.Timestamp {
		result.Timestamp = time.Now().UTC().Format(time.RFC3339)
	}
	if template.Thumbnail != "" && !strings.Contains(template.Thumbnail, "{") {
		if parsed, err := url.Parse(template.Thumbnail); err == nil && parsed.Host != "" && (parsed.Scheme == "http" || parsed.Scheme == "https") {
			result.Thumbnail = &discordgo.MessageEmbedThumbnail{URL: template.Thumbnail}
		}
	}
	if template.Image != "" && !strings.Contains(template.Image, "{") {
		if parsed, err := url.Parse(template.Image); err == nil && parsed.Host != "" && (parsed.Scheme == "http" || parsed.Scheme == "https") {
			result.Image = &discordgo.MessageEmbedImage{URL: template.Image}
		}
	}
	if template.Author != "" {
		result.Author = &discordgo.MessageEmbedAuthor{Name: template.Author, URL: previewURL(template.AuthorURL)}
		result.Author.IconURL = previewURL(template.AuthorIcon)
	}
	if template.Footer != "" {
		result.Footer = &discordgo.MessageEmbedFooter{Text: template.Footer, IconURL: previewURL(template.FooterIcon)}
	}
	for _, field := range template.Fields {
		result.Fields = append(result.Fields, &discordgo.MessageEmbedField{Name: field.Name, Value: field.Value, Inline: field.Inline})
	}
	if embedIsEmpty(result) {
		return nil, fmt.Errorf("embed is empty")
	}
	if err := Validate(result); err != nil {
		return nil, err
	}
	return result, nil
}

func previewURL(value string) string {
	if value == "" || strings.Contains(value, "{") {
		return ""
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return ""
	}
	return value
}

func Build(template Template, vars Variables) (MessagePayload, error) {
	embed, err := Render(template, vars)
	if err != nil {
		return MessagePayload{}, err
	}
	components, err := buildButtons(template.Buttons, vars)
	if err != nil {
		return MessagePayload{}, err
	}
	content := resolveText(template.Content, vars)
	if discordLen(content) > 2000 {
		return MessagePayload{}, fmt.Errorf("message content exceeds 2000 characters")
	}
	embeds := []*discordgo.MessageEmbed{embed}
	if embedIsEmpty(embed) {
		embeds = nil
	}
	if content == "" && len(embeds) == 0 && len(components) == 0 {
		return MessagePayload{}, fmt.Errorf("template must contain content, an embed, or a button")
	}
	return MessagePayload{
		Content: content, Embeds: embeds, Components: components,
		AllowedMentions: &discordgo.MessageAllowedMentions{},
	}, nil
}

// ValidateTemplate checks a saved template without requiring a runtime
// member/guild context. Empty templates are allowed while they are being
// edited in the visual panel.
func ValidateTemplate(template Template) error {
	if discordLen(template.Content) > 2000 {
		return fmt.Errorf("message content exceeds 2000 characters")
	}
	if _, err := Render(template, validationVariables()); err != nil {
		return err
	}
	_, err := buildButtons(template.Buttons, validationVariables())
	return err
}

func validationVariables() Variables {
	const placeholder = "https://example.com/asset.png"
	return Variables{
		UserID: "1", UserName: "user", UserMention: "<@1>", UserAvatar: placeholder, GuildIcon: placeholder,
		RuleName: "rule", RuleSource: "vanity", RuleValue: "value", RuleCondition: "condition", RuleReason: "reason",
		RoleID: "1", VanityRule: "rule", VanityWord: "word", VanitySource: "username", VanityValue: "value",
		TagRule: "rule", TagCondition: "condition", Tag: "tag", TagRuleValue: "value", TagGuildID: "1", TagEnabled: "true",
		Action: "add_role", ActionText: "add", Result: "completed", EventName: "vanity_add", EventTitle: "Vanity Action", EventError: "error", MatchedValue: "value",
	}
}

func buildButtons(rows [][]TemplateButton, vars Variables) ([]discordgo.MessageComponent, error) {
	if len(rows) > 5 {
		return nil, fmt.Errorf("embed template has more than 5 button rows")
	}
	components := make([]discordgo.MessageComponent, 0, len(rows))
	for _, row := range rows {
		if len(row) == 0 || len(row) > 5 {
			return nil, fmt.Errorf("each button row must contain 1 to 5 buttons")
		}
		buttons := make([]discordgo.MessageComponent, 0, len(row))
		for _, item := range row {
			label := resolveText(item.Label, vars)
			if label == "" || discordLen(label) > 80 {
				return nil, fmt.Errorf("button label must contain 1 to 80 characters")
			}
			value, err := resolveURL(item.URL, vars, "button URL")
			if err != nil {
				return nil, err
			}
			buttons = append(buttons, &discordgo.Button{Style: discordgo.LinkButton, Label: label, URL: value, Disabled: item.Disabled})
		}
		components = append(components, &discordgo.ActionsRow{Components: buttons})
	}
	return components, nil
}

func Validate(embed *discordgo.MessageEmbed) error {
	if embed == nil {
		return fmt.Errorf("embed is nil")
	}
	if discordLen(embed.Title) > 256 || discordLen(embed.Description) > 4096 {
		return fmt.Errorf("embed title or description exceeds Discord limits")
	}
	if embed.Color < 0 || embed.Color > 0xFFFFFF {
		return fmt.Errorf("embed color must be between #000000 and #FFFFFF")
	}
	if len(embed.Fields) > 25 {
		return fmt.Errorf("embed has more than 25 fields")
	}
	total := discordLen(embed.Title) + discordLen(embed.Description)
	if embed.Footer != nil {
		if discordLen(embed.Footer.Text) > 2048 {
			return fmt.Errorf("embed footer exceeds 2048 characters")
		}
		total += discordLen(embed.Footer.Text)
	}
	if embed.Author != nil {
		if discordLen(embed.Author.Name) > 256 {
			return fmt.Errorf("embed author exceeds 256 characters")
		}
		total += discordLen(embed.Author.Name)
	}
	for _, field := range embed.Fields {
		if field == nil || discordLen(field.Name) == 0 || discordLen(field.Value) == 0 {
			return fmt.Errorf("embed fields require a name and value")
		}
		if discordLen(field.Name) > 256 || discordLen(field.Value) > 1024 {
			return fmt.Errorf("embed field exceeds Discord limits")
		}
		total += discordLen(field.Name) + discordLen(field.Value)
	}
	if total > 6000 {
		return fmt.Errorf("embed exceeds Discord's 6000-character aggregate limit")
	}
	return nil
}

func resolveText(value string, vars Variables) string {
	replace := map[string]string{
		"{user}": vars.UserMention, "{user.mention}": vars.UserMention,
		"{user.id}": vars.UserID, "{user.name}": vars.UserName,
		"{user.tag}": vars.UserName, "{user.avatar}": vars.UserAvatar,
		"{user.display_avatar}": vars.UserAvatar, "{user.display_name}": vars.UserDisplayName,
		"{guild}": vars.GuildName, "{guild.name}": vars.GuildName,
		"{guild.id}": vars.GuildID, "{guild.icon}": vars.GuildIcon,
		"{server_name}": vars.GuildName, "{server.id}": vars.GuildID, "{server_id}": vars.GuildID,
		"{server_icon}": vars.GuildIcon, "{channel.id}": vars.ChannelID,
		"{channel.name}": vars.ChannelName, "{rule.name}": vars.RuleName,
		"{rule.source}": vars.RuleSource, "{rule.value}": vars.RuleValue,
		"{rule.condition}": vars.RuleCondition, "{rule.reason}": vars.RuleReason,
		"{role}": mentionRole(vars.RoleID), "{role.id}": vars.RoleID,
		"{action}": vars.Action, "{action.text}": vars.ActionText, "{result}": vars.Result,
		"{event}": vars.EventName, "{event.name}": vars.EventName, "{event.title}": vars.EventTitle,
		"{event.error}": vars.EventError, "{event.matched_value}": vars.MatchedValue,
		"{vanity.rule}": vars.VanityRule, "{vanity.word}": vars.VanityWord,
		"{vanity.source}": vars.VanitySource, "{vanity.value}": vars.VanityValue,
		"{vanity.role}": mentionRole(vars.VanityRoleID), "{vanity.role.id}": vars.VanityRoleID,
		"{tag.rule}": vars.TagRule, "{tag.condition}": vars.TagCondition,
		"{tag}": vars.Tag, "{tag.value}": vars.Tag, "{tag.rule_value}": vars.TagRuleValue,
		"{tag.guild_id}": vars.TagGuildID, "{tag.enabled}": vars.TagEnabled,
		"{tag.badge}": vars.TagBadge, "{tag.role}": mentionRole(vars.TagRoleID), "{tag.role.id}": vars.TagRoleID,
		"{identity.source}": vars.RuleSource, "{identity.value}": vars.RuleValue,
		"{timestamp}": formatTimestamp(vars.Timestamp), "{newline}": "\n",
		"{separator}": "──────────────────────", "{date.utc_timestamp}": fmt.Sprintf("%d", time.Now().Unix()),
	}
	value = variablePattern.ReplaceAllStringFunc(value, func(token string) string {
		if replacement, ok := replace[strings.ToLower(token)]; ok {
			return replacement
		}
		return token
	})
	return escapeMentions(value)
}

func resolveURL(value string, vars Variables, label string) (string, error) {
	resolved := resolveText(value, vars)
	if resolved == "" && strings.Contains(value, "{") {
		return "", nil
	}
	parsed, err := url.Parse(resolved)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") {
		return "", fmt.Errorf("%s must be an http(s) URL", label)
	}
	return resolved, nil
}

func formatTimestamp(value time.Time) string {
	if value.IsZero() {
		return time.Now().UTC().Format(time.RFC3339)
	}
	return value.UTC().Format(time.RFC3339)
}

func embedIsEmpty(embed *discordgo.MessageEmbed) bool {
	return embed.Title == "" && embed.Description == "" && embed.URL == "" && embed.Footer == nil && embed.Author == nil && embed.Image == nil && embed.Thumbnail == nil && len(embed.Fields) == 0
}

func escapeMentions(value string) string {
	value = strings.ReplaceAll(value, "@everyone", "@​everyone")
	value = strings.ReplaceAll(value, "@here", "@​here")
	return value
}

func mentionRole(roleID string) string {
	if roleID == "" {
		return ""
	}
	return "<@&" + roleID + ">"
}

func discordLen(value string) int {
	return len(utf16.Encode([]rune(value)))
}

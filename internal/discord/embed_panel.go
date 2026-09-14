package discord

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/PettoBot/vanity-tag-bot/internal/database"
	"github.com/PettoBot/vanity-tag-bot/internal/embeds"
	"github.com/bwmarrin/discordgo"
)

const embedPanelPrefix = "et"

func (b *Bot) embedPanelResponse(ctx context.Context, item database.EmbedTemplate, ownerID string, variables embeds.Variables) (*discordgo.InteractionResponseData, error) {
	var template embeds.Template
	if err := json.Unmarshal(item.Payload, &template); err != nil {
		return nil, fmt.Errorf("template payload is invalid: %w", err)
	}
	b.cacheEmbedPanel(ownerID, item.ID, template)
	preview, err := embeds.Render(template, variables)
	if err == nil && embedPreviewEmpty(preview) {
		err = fmt.Errorf("embed is empty")
	}
	if err != nil {
		preview, err = embeds.Preview(template)
	}
	if err != nil {
		description := "The preview is empty. Use the buttons below to add a title, description or content."
		if templateHasVisualContent(template) {
			description = "The current template has an invalid field or variable. Fix it with the editor below."
		}
		preview = &discordgo.MessageEmbed{Color: 0x4B4F59, Description: description}
	}
	content := embedEditorContent(item.Name, b.config.Emojis["STAR"], b.config.WebsiteURL, b.config.DocsURL)
	return &discordgo.InteractionResponseData{
		Content:         content,
		Flags:           discordgo.MessageFlagsEphemeral,
		Embeds:          []*discordgo.MessageEmbed{preview},
		Components:      embedPanelComponents(item.ID, ownerID, template, b.config.Emojis),
		AllowedMentions: &discordgo.MessageAllowedMentions{},
	}, nil
}

func embedPreviewEmpty(embed *discordgo.MessageEmbed) bool {
	return embed == nil || (embed.Title == "" && embed.Description == "" && embed.URL == "" &&
		embed.Footer == nil && embed.Author == nil && embed.Image == nil && embed.Thumbnail == nil &&
		len(embed.Fields) == 0)
}

func embedEditorContent(name, rawEmoji, websiteURL, docsURL string) string {
	emoji := emojiText(rawEmoji, "⭐")
	if name == "vanity_notify" || name == "guildtag_notify" {
		command := "/vanity notify"
		source := "Vanity"
		if name == "guildtag_notify" {
			command = "/guildtag notify"
			source = "Guild Tag"
		}
		return fmt.Sprintf("%s **%s thank-you message editor**\nEmbed: `%s`\nThis message is sent when a matching rule starts applying, even if the member already has the role. Configure its channel and mention setting with `%s`.\n\n**Workflow**\n1. Choose a section below.\n2. Submit the modal; that change is saved immediately.\n3. Press **Done** when finished, then use `/embed preview name:%s`.\n\nThe preview resolves the current member's values. The notification resolves them again when it is sent.%s", emoji, source, name, command, name, editorLinks(websiteURL, docsURL))
	}
	if strings.HasPrefix(name, "notify_") {
		return fmt.Sprintf("%s **Legacy notification embed**\nEmbed: `%s`\nThis old embed is not used by `/logs` anymore. Configure user notifications with `/vanity notify` or `/guildtag notify`.\n\nYou can keep editing it, but the active embeds are `vanity_notify` and `guildtag_notify`.%s", emoji, name, editorLinks(websiteURL, docsURL))
	}
	return fmt.Sprintf("%s **Embed editor**\nTemplate: `%s`\nEdit one section at a time. The preview updates after every submitted change.\n\n**Workflow**\n1. Choose a section.\n2. Submit the modal; that change is saved immediately.\n3. Press **Done** when finished.\n\nVariables resolve in preview, send and automatic notifications.%s", emoji, name, editorLinks(websiteURL, docsURL))
}

func editorLinks(websiteURL, docsURL string) string {
	links := make([]string, 0, 2)
	if websiteURL != "" {
		links = append(links, "[Web]("+websiteURL+")")
	}
	if docsURL != "" {
		links = append(links, "[Wiki]("+docsURL+")")
	}
	if len(links) == 0 {
		return ""
	}
	return "\n\n" + strings.Join(links, " • ")
}

func templateHasVisualContent(template embeds.Template) bool {
	return template.Content != "" || template.Title != "" || template.Description != "" || template.URL != "" ||
		template.Thumbnail != "" || template.Image != "" || template.Timestamp || template.Footer != "" ||
		template.Author != "" || len(template.Fields) > 0
}

func embedPanelComponents(templateID, ownerID string, template embeds.Template, emojis map[string]string) []discordgo.MessageComponent {
	button := func(action, label string, style discordgo.ButtonStyle, emoji string) *discordgo.Button {
		value := &discordgo.Button{Style: style, Label: label, CustomID: embedPanelID(action, ownerID, templateID)}
		if parsed := parseEmoji(emoji); parsed != nil {
			value.Emoji = parsed
		}
		return value
	}
	timestampLabel := "Timestamp"
	if template.Timestamp {
		timestampLabel = "Timestamp ✓"
	}
	return []discordgo.MessageComponent{
		&discordgo.ActionsRow{Components: []discordgo.MessageComponent{
			button("title", "Title", discordgo.SecondaryButton, ""),
			button("description", "Description", discordgo.SecondaryButton, ""),
			button("content", "Message text", discordgo.SecondaryButton, ""),
			button("color", "Color", discordgo.SecondaryButton, ""),
			button("author", "Author", discordgo.SecondaryButton, ""),
		}},
		&discordgo.ActionsRow{Components: []discordgo.MessageComponent{
			button("footer", "Footer", discordgo.SecondaryButton, ""),
			button("thumbnail", "Thumbnail", discordgo.SecondaryButton, ""),
			button("image", "Image", discordgo.SecondaryButton, ""),
			button("timestamp", timestampLabel, discordgo.SecondaryButton, ""),
			button("field_add", "Add field", discordgo.SecondaryButton, ""),
		}},
		&discordgo.ActionsRow{Components: []discordgo.MessageComponent{
			button("field_remove", "Remove field", discordgo.SecondaryButton, ""),
			button("button_add", "Add link button", discordgo.SecondaryButton, ""),
			button("button_clear", "Remove link buttons", discordgo.DangerButton, ""),
		}},
		&discordgo.ActionsRow{Components: []discordgo.MessageComponent{
			button("save", "Done", discordgo.SuccessButton, emojis["APPROVE"]),
			button("reset", "Reset template", discordgo.DangerButton, emojis["ALERT"]),
			button("cancel", "Close", discordgo.SecondaryButton, emojis["DENY"]),
		}},
	}
}

func embedPanelID(action, ownerID, templateID string) string {
	return fmt.Sprintf("%s:%s:%s:%s", embedPanelPrefix, action, ownerID, templateID)
}

func parseEmbedPanelID(customID string) (action, ownerID, templateID string, ok bool) {
	parts := strings.Split(customID, ":")
	if len(parts) != 4 || parts[0] != embedPanelPrefix || parts[1] == "" || parts[2] == "" || parts[3] == "" {
		return "", "", "", false
	}
	return parts[1], parts[2], parts[3], true
}

func (b *Bot) handleEmbedPanelButton(event *discordgo.InteractionCreate) {
	data := event.MessageComponentData()
	action, ownerID, templateID, ok := parseEmbedPanelID(data.CustomID)
	if !ok {
		respond(event, "This embed editor has expired. Run `/embed create` or `/embed edit` again.", true)
		return
	}
	if actorID(event) != ownerID {
		respond(event, "This embed editor belongs to another administrator.", true)
		return
	}
	if isEmbedModalAction(action) {
		// A modal must be acknowledged immediately. The panel cache contains the
		// values used to render this editor, so opening it never waits on Postgres.
		template := b.cachedEmbedPanel(ownerID, templateID)
		if err := showEmbedModal(event, action, ownerID, templateID, template); err != nil {
			b.logger.Error("open embed editor modal failed", "action", action, "error", err)
			respond(event, "Discord could not open this editor: "+err.Error(), true)
		}
		return
	}
	if !isEmbedPanelAction(action) {
		respond(event, "Unknown embed editor action.", true)
		return
	}
	if !b.deferEmbedPanel(event) {
		return
	}
	ctx, cancel := b.operationContext()
	defer cancel()
	item, err := b.store.GetEmbedTemplateByID(ctx, event.GuildID, templateID)
	if err != nil {
		b.updateEmbedPanel(event, embedPanelError("Embed template not found. It may have been deleted."))
		return
	}
	var template embeds.Template
	if err := json.Unmarshal(item.Payload, &template); err != nil {
		b.updateEmbedPanel(event, embedPanelError("Embed template payload is invalid."))
		return
	}

	switch action {
	case "save":
		content := fmt.Sprintf("Done. Changes for `%s` were already saved when you submitted each section. The template is ready for `/embed preview` or `/embed send`.", item.Name)
		b.updateEmbedPanel(event, &discordgo.InteractionResponseData{Content: content, Components: []discordgo.MessageComponent{}, Embeds: []*discordgo.MessageEmbed{}, AllowedMentions: &discordgo.MessageAllowedMentions{}})
		return
	case "cancel":
		content := "Embed editor closed. Changes already saved by the editor remain available."
		b.updateEmbedPanel(event, &discordgo.InteractionResponseData{Content: content, Components: []discordgo.MessageComponent{}, Embeds: []*discordgo.MessageEmbed{}, AllowedMentions: &discordgo.MessageAllowedMentions{}})
		return
	case "reset":
		template = embeds.Template{}
		if err := b.saveEmbedTemplate(ctx, item, template); err != nil {
			b.updateEmbedPanel(event, embedPanelError("Could not reset the template: "+err.Error()))
			return
		}
	case "timestamp":
		template.Timestamp = !template.Timestamp
		if err := b.saveEmbedTemplate(ctx, item, template); err != nil {
			b.updateEmbedPanel(event, embedPanelError("Could not update the timestamp: "+err.Error()))
			return
		}
	case "button_clear":
		template.Buttons = nil
		if err := b.saveEmbedTemplate(ctx, item, template); err != nil {
			b.updateEmbedPanel(event, embedPanelError("Could not clear the buttons: "+err.Error()))
			return
		}
	}

	updated, err := b.store.GetEmbedTemplateByID(ctx, event.GuildID, templateID)
	if err != nil {
		b.updateEmbedPanel(event, embedPanelError("Embed template was saved but could not be reloaded."))
		return
	}
	response, err := b.embedPanelResponse(ctx, updated, ownerID, b.embedVariables(ctx, event))
	if err != nil {
		b.updateEmbedPanel(event, embedPanelError(err.Error()))
		return
	}
	b.updateEmbedPanel(event, response)
}

func isEmbedModalAction(action string) bool {
	switch action {
	case "title", "description", "content", "color", "author", "footer", "thumbnail", "image", "field_add", "field_remove", "button_add":
		return true
	default:
		return false
	}
}

func isEmbedPanelAction(action string) bool {
	switch action {
	case "save", "cancel", "reset", "timestamp", "button_clear":
		return true
	default:
		return false
	}
}

func (b *Bot) cacheEmbedPanel(ownerID, templateID string, template embeds.Template) {
	b.embedPanelMu.Lock()
	if b.embedPanels == nil {
		b.embedPanels = make(map[string]embeds.Template)
	}
	b.embedPanels[embedPanelCacheKey(ownerID, templateID)] = template
	b.embedPanelMu.Unlock()
}

func (b *Bot) cachedEmbedPanel(ownerID, templateID string) embeds.Template {
	b.embedPanelMu.Lock()
	template := b.embedPanels[embedPanelCacheKey(ownerID, templateID)]
	b.embedPanelMu.Unlock()
	return template
}

func embedPanelCacheKey(ownerID, templateID string) string {
	return ownerID + ":" + templateID
}

func showEmbedModal(event *discordgo.InteractionCreate, action, ownerID, templateID string, template embeds.Template) error {
	modal := &discordgo.InteractionResponseData{
		CustomID: embedModalID(action, ownerID, templateID),
		Title:    "Edit embed " + action,
	}
	add := func(id, label, value string, style discordgo.TextInputStyle, required bool, max int) {
		modal.Components = append(modal.Components, &discordgo.ActionsRow{Components: []discordgo.MessageComponent{&discordgo.TextInput{
			CustomID: id, Label: label, Style: style, Required: required, MaxLength: max, Value: value,
		}}})
	}
	switch action {
	case "title":
		add("title", "Title", template.Title, discordgo.TextInputShort, false, 256)
		add("url", "Title URL (optional, https://)", template.URL, discordgo.TextInputShort, false, 2048)
	case "description":
		// Discord modal text inputs are capped at 4000 characters, even though
		// the resulting embed description can be 4096 characters.
		add("description", "Description", template.Description, discordgo.TextInputParagraph, false, 4000)
	case "content":
		add("content", "Message content", template.Content, discordgo.TextInputParagraph, false, 2000)
	case "color":
		value := ""
		if template.Color > 0 {
			value = fmt.Sprintf("#%06X", template.Color)
		}
		add("color", "Hex color, for example #5865F2", value, discordgo.TextInputShort, false, 7)
	case "author":
		add("name", "Author name", template.Author, discordgo.TextInputShort, false, 256)
		add("icon", "Author icon URL", template.AuthorIcon, discordgo.TextInputShort, false, 2048)
		add("url", "Author URL", template.AuthorURL, discordgo.TextInputShort, false, 2048)
	case "footer":
		add("text", "Footer text", template.Footer, discordgo.TextInputShort, false, 2048)
		add("icon", "Footer icon URL", template.FooterIcon, discordgo.TextInputShort, false, 2048)
	case "thumbnail":
		add("url", "Thumbnail URL", template.Thumbnail, discordgo.TextInputShort, false, 2048)
	case "image":
		add("url", "Image URL", template.Image, discordgo.TextInputShort, false, 2048)
	case "field_add":
		add("name", "Field name", "", discordgo.TextInputShort, true, 256)
		add("value", "Field value", "", discordgo.TextInputParagraph, true, 1024)
		add("inline", "Inline? write yes or no", "no", discordgo.TextInputShort, false, 3)
	case "field_remove":
		add("position", "Field position, 1 to 25", "", discordgo.TextInputShort, true, 2)
	case "button_add":
		add("label", "Button label", "", discordgo.TextInputShort, true, 80)
		add("url", "Button URL", "", discordgo.TextInputShort, true, 2048)
		add("row", "Row 1 to 5", "1", discordgo.TextInputShort, false, 1)
	default:
		return fmt.Errorf("unknown embed editor modal")
	}
	return interactionRespond(nil, event, modal, discordgo.InteractionResponseModal)
}

func embedModalID(action, ownerID, templateID string) string {
	return fmt.Sprintf("%s:%s:%s:%s", embedPanelPrefix+"m", action, ownerID, templateID)
}

func parseEmbedModalID(customID string) (action, ownerID, templateID string, ok bool) {
	parts := strings.Split(customID, ":")
	if len(parts) != 4 || parts[0] != embedPanelPrefix+"m" || parts[1] == "" || parts[2] == "" || parts[3] == "" {
		return "", "", "", false
	}
	return parts[1], parts[2], parts[3], true
}

func (b *Bot) handleEmbedPanelModal(event *discordgo.InteractionCreate) {
	action, ownerID, templateID, ok := parseEmbedModalID(event.ModalSubmitData().CustomID)
	if !ok {
		return
	}
	if actorID(event) != ownerID {
		respond(event, "This embed editor belongs to another administrator.", true)
		return
	}
	// Modal submits cannot use the component update callback type. Keep the
	// database work bounded by the interaction deadline and return the updated
	// editor as the modal's original response.
	if err := interactionRespond(b.session, event, &discordgo.InteractionResponseData{Flags: discordgo.MessageFlagsEphemeral}, discordgo.InteractionResponseDeferredChannelMessageWithSource); err != nil {
		b.logger.Error("defer embed editor modal failed", "error", err)
		return
	}
	ctx, cancel := b.operationContext()
	defer cancel()
	item, err := b.store.GetEmbedTemplateByID(ctx, event.GuildID, templateID)
	if err != nil {
		b.updateEmbedPanel(event, embedPanelError("Embed template not found."))
		return
	}
	var template embeds.Template
	if err := json.Unmarshal(item.Payload, &template); err != nil {
		b.updateEmbedPanel(event, embedPanelError("Embed template payload is invalid."))
		return
	}
	values := modalValues(event.ModalSubmitData().Components)
	if err := applyEmbedModal(&template, action, values); err != nil {
		b.updateEmbedPanel(event, embedPanelError(err.Error()))
		return
	}
	if err := b.saveEmbedTemplate(ctx, item, template); err != nil {
		b.updateEmbedPanel(event, embedPanelError("Could not save the embed: "+err.Error()))
		return
	}
	updated, err := b.store.GetEmbedTemplateByID(ctx, event.GuildID, templateID)
	if err != nil {
		b.updateEmbedPanel(event, embedPanelError("Embed saved but could not be reloaded."))
		return
	}
	response, err := b.embedPanelResponse(ctx, updated, ownerID, b.embedVariables(ctx, event))
	if err != nil {
		b.updateEmbedPanel(event, embedPanelError(err.Error()))
		return
	}
	b.updateEmbedPanel(event, response)
}

func (b *Bot) deferEmbedPanel(event *discordgo.InteractionCreate) bool {
	if err := interactionRespond(b.session, event, &discordgo.InteractionResponseData{}, discordgo.InteractionResponseDeferredMessageUpdate); err != nil {
		b.logger.Error("defer embed editor update failed", "error", err)
		return false
	}
	return true
}

func (b *Bot) updateEmbedPanel(event *discordgo.InteractionCreate, response *discordgo.InteractionResponseData) {
	content := response.Content
	embedsValue := response.Embeds
	components := response.Components
	if err := func() error {
		_, err := b.session.InteractionResponseEdit(event.Interaction, &discordgo.WebhookEdit{
			Content: &content, Embeds: &embedsValue, Components: &components, AllowedMentions: response.AllowedMentions,
		})
		return err
	}(); err != nil {
		b.logger.Error("update embed editor failed", "error", err)
	}
}

func embedPanelError(content string) *discordgo.InteractionResponseData {
	return &discordgo.InteractionResponseData{
		Content:         content,
		Embeds:          []*discordgo.MessageEmbed{},
		Components:      []discordgo.MessageComponent{},
		AllowedMentions: &discordgo.MessageAllowedMentions{},
	}
}

func applyEmbedModal(template *embeds.Template, action string, values map[string]string) error {
	get := func(key string) string { return strings.TrimSpace(values[key]) }
	switch action {
	case "title":
		template.Title, template.URL = get("title"), get("url")
	case "description":
		template.Description = get("description")
	case "content":
		template.Content = get("content")
	case "color":
		value := strings.TrimPrefix(strings.TrimSpace(get("color")), "#")
		if value == "" {
			template.Color = 0
			break
		}
		if len(value) != 6 {
			return fmt.Errorf("color must be a six-digit hex value such as `#5865F2`")
		}
		parsed, err := strconv.ParseInt(value, 16, 32)
		if err != nil || parsed < 0 || parsed > 0xFFFFFF {
			return fmt.Errorf("color must be a six-digit hex value such as `#5865F2`")
		}
		template.Color = int(parsed)
	case "author":
		template.Author, template.AuthorIcon, template.AuthorURL = get("name"), get("icon"), get("url")
	case "footer":
		template.Footer, template.FooterIcon = get("text"), get("icon")
	case "thumbnail":
		template.Thumbnail = get("url")
	case "image":
		template.Image = get("url")
	case "field_add":
		if len(template.Fields) >= 25 {
			return fmt.Errorf("an embed can have at most 25 fields")
		}
		name, value := get("name"), get("value")
		if name == "" || value == "" {
			return fmt.Errorf("field name and value are required")
		}
		template.Fields = append(template.Fields, embeds.TemplateField{Name: name, Value: value, Inline: strings.EqualFold(get("inline"), "yes")})
	case "field_remove":
		position, err := strconv.Atoi(get("position"))
		if err != nil || position < 1 || position > len(template.Fields) {
			return fmt.Errorf("field position is invalid")
		}
		template.Fields = append(template.Fields[:position-1], template.Fields[position:]...)
	case "button_add":
		row, err := strconv.Atoi(get("row"))
		if err != nil || row < 1 || row > 5 {
			return fmt.Errorf("button row must be between 1 and 5")
		}
		for len(template.Buttons) < row {
			template.Buttons = append(template.Buttons, []embeds.TemplateButton{})
		}
		if len(template.Buttons[row-1]) >= 5 {
			return fmt.Errorf("that button row already has 5 buttons")
		}
		label, value := get("label"), get("url")
		if label == "" || value == "" {
			return fmt.Errorf("button label and URL are required")
		}
		template.Buttons[row-1] = append(template.Buttons[row-1], embeds.TemplateButton{Label: label, URL: value})
	}
	return nil
}

func (b *Bot) saveEmbedTemplate(ctx context.Context, item database.EmbedTemplate, template embeds.Template) error {
	if err := embeds.ValidateTemplate(template); err != nil {
		return err
	}
	payload, err := json.Marshal(template)
	if err != nil {
		return err
	}
	item.Payload = payload
	return b.store.SaveEmbedTemplate(ctx, item)
}

func parseEmoji(value string) *discordgo.ComponentEmoji {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	if strings.HasPrefix(value, "<:") || strings.HasPrefix(value, "<a:") {
		parts := strings.Split(strings.Trim(value, "<>"), ":")
		if len(parts) == 3 {
			return &discordgo.ComponentEmoji{Name: parts[1], ID: parts[2], Animated: strings.HasPrefix(value, "<a:")}
		}
	}
	if strings.HasPrefix(value, ":") && strings.HasSuffix(value, ":") {
		return nil
	}
	return &discordgo.ComponentEmoji{Name: value}
}

func emojiText(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	// `:name:` is only a textual alias and Discord will display it literally
	// unless it is converted to `<:name:id>`. Use the Unicode fallback instead
	// of leaking the raw alias into the panel.
	if strings.HasPrefix(value, ":") && strings.HasSuffix(value, ":") {
		return fallback
	}
	return value
}

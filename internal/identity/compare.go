package identity

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

const maxRegexLength = 256
const maxVanityValueLength = 512

func Normalize(value string, options Normalization) string {
	if options.TrimSpace {
		value = strings.TrimSpace(value)
	}
	if options.CollapseSpace {
		var builder strings.Builder
		space := false
		for _, r := range value {
			if unicode.IsSpace(r) {
				if !space {
					builder.WriteByte(' ')
				}
				space = true
				continue
			}
			builder.WriteRune(r)
			space = false
		}
		value = builder.String()
	}
	if options.CaseFold {
		value = strings.ToLower(value)
	}
	return value
}

func ResolveDisplayName(member MemberIdentity) string {
	if member.GuildNickname != "" {
		return member.GuildNickname
	}
	if member.GlobalName != "" {
		return member.GlobalName
	}
	if member.DisplayName != "" {
		return member.DisplayName
	}
	return member.Username
}

func VanityValue(member MemberIdentity, source VanitySource) string {
	switch source {
	case VanityUsername:
		return member.Username
	case VanityGlobalName:
		return member.GlobalName
	case VanityGuildNickname:
		return member.GuildNickname
	case VanityDisplayName:
		return ResolveDisplayName(member)
	case VanityCustomStatus:
		return member.CustomStatus
	default:
		return ""
	}
}

func MatchVanity(rule VanityRule, member MemberIdentity) (bool, error) {
	if rule.Word == "" {
		return false, fmt.Errorf("vanity rule %q has an empty word", rule.Name)
	}
	left := Normalize(VanityValue(member, rule.Source), rule.Normalization)
	right := Normalize(rule.Word, rule.Normalization)
	switch rule.Comparison {
	case ComparisonEquals:
		return left == right, nil
	case ComparisonContains:
		return strings.Contains(left, right), nil
	case ComparisonStartsWith:
		return strings.HasPrefix(left, right), nil
	case ComparisonEndsWith:
		return strings.HasSuffix(left, right), nil
	case ComparisonRegex:
		if len([]rune(rule.Word)) > maxRegexLength {
			return false, fmt.Errorf("regex rule %q exceeds %d characters", rule.Name, maxRegexLength)
		}
		if len([]rune(left)) > maxVanityValueLength {
			return false, fmt.Errorf("value for regex rule %q exceeds %d characters", rule.Name, maxVanityValueLength)
		}
		// Go's regexp package uses a RE2-style linear-time engine. The bounded
		// pattern and input keep evaluation predictable without an unsafe
		// backtracking engine or an unbounded goroutine.
		expression, err := regexp.Compile(right)
		if err != nil {
			return false, fmt.Errorf("invalid regex in rule %q: %w", rule.Name, err)
		}
		return expression.MatchString(left), nil
	default:
		return false, fmt.Errorf("unsupported vanity comparison %q", rule.Comparison)
	}
}

func MatchGuildTag(rule GuildTagRule, primary *PrimaryGuild) bool {
	identityGuildID := ""
	identityEnabled := false
	tag := ""
	if primary != nil {
		identityGuildID = primary.IdentityGuildID
		identityEnabled = primary.IdentityEnabled != nil && *primary.IdentityEnabled
		tag = primary.Tag
	}
	switch rule.Condition {
	case ConditionIsGuildID:
		return identityGuildID == rule.Value
	case ConditionIsNotGuildID:
		return identityGuildID != rule.Value
	case ConditionIdentityEnabled:
		return identityEnabled
	case ConditionIdentityDisabled:
		return !identityEnabled
	case ConditionTagEquals:
		return strings.EqualFold(tag, rule.Value)
	case ConditionTagNotEquals:
		return !strings.EqualFold(tag, rule.Value)
	default:
		return false
	}
}

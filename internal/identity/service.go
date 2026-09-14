package identity

import "context"

type Service struct {
	Store  Store
	Engine *Engine
}

func NewService(store Store, roles RoleClient) *Service {
	return &Service{Store: store, Engine: &Engine{Store: store, Roles: roles}}
}

func (s *Service) EvaluateMember(ctx context.Context, member MemberIdentity) ([]Evaluation, error) {
	vanity, err := s.Store.ListVanityRules(ctx, member.GuildID)
	if err != nil {
		return nil, err
	}
	guildTags, err := s.Store.ListGuildTagRules(ctx, member.GuildID)
	if err != nil {
		return nil, err
	}
	return s.Engine.Evaluate(ctx, member, vanity, guildTags)
}

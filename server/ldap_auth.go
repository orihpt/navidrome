package server

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/go-ldap/ldap/v3"
	"github.com/navidrome/navidrome/conf"
	"github.com/navidrome/navidrome/consts"
	"github.com/navidrome/navidrome/log"
	"github.com/navidrome/navidrome/model"
	"github.com/navidrome/navidrome/model/id"
)

type ldapUser struct {
	username string
	name     string
	email    string
}

func authenticateLDAPAndSyncUser(ctx context.Context, userRepo model.UserRepository, username, password string) (*model.User, error) {
	if !conf.Server.LDAP.Enabled {
		return nil, nil
	}
	if strings.TrimSpace(password) == "" {
		return nil, nil
	}

	ldapUser, err := authenticateLDAP(ctx, username, password)
	if err != nil {
		if errors.Is(err, model.ErrInvalidAuth) {
			return nil, nil
		}
		return nil, err
	}

	user, err := userRepo.FindByUsernameWithPassword(ldapUser.username)
	if errors.Is(err, model.ErrNotFound) {
		if !conf.Server.LDAP.AutoCreateUsers {
			return nil, nil
		}
		user = &model.User{
			ID:          id.NewRandom(),
			UserName:    ldapUser.username,
			Name:        firstNonEmpty(ldapUser.name, ldapUser.username),
			Email:       ldapUser.email,
			NewPassword: consts.PasswordAutogenPrefix + id.NewRandom(),
			IsAdmin:     false,
		}
		if err := userRepo.Put(user); err != nil {
			return nil, fmt.Errorf("creating LDAP user in DB: %w", err)
		}
		user, err = userRepo.FindByUsernameWithPassword(ldapUser.username)
	}
	if err != nil {
		return nil, err
	}
	if err := userRepo.UpdateLastLoginAt(user.ID); err != nil {
		log.Error(ctx, "Could not update LastLoginAt", "user", user.UserName, err)
	}
	return user, nil
}

func authenticateLDAP(ctx context.Context, username, password string) (*ldapUser, error) {
	cfg := conf.Server.LDAP
	if cfg.URL == "" || cfg.BaseDN == "" {
		return nil, fmt.Errorf("LDAP is enabled but ldap.url or ldap.basedn is empty")
	}

	conn, err := ldap.DialURL(cfg.URL, ldap.DialWithTLSConfig(ldapTLSConfig(cfg.URL)))
	if err != nil {
		return nil, fmt.Errorf("connecting to LDAP: %w", err)
	}
	defer conn.Close()

	if cfg.StartTLS {
		if err := conn.StartTLS(ldapTLSConfig(cfg.URL)); err != nil {
			return nil, fmt.Errorf("starting LDAP TLS: %w", err)
		}
	}

	if cfg.BindDN != "" || cfg.BindPassword != "" {
		if err := conn.Bind(cfg.BindDN, cfg.BindPassword); err != nil {
			return nil, fmt.Errorf("binding LDAP service account: %w", err)
		}
	}

	filter := fmt.Sprintf(firstNonEmpty(cfg.UserFilter, "(uid=%s)"), ldap.EscapeFilter(username))
	attrs := []string{firstNonEmpty(cfg.UserNameAttribute, "uid"), firstNonEmpty(cfg.NameAttribute, "cn"), firstNonEmpty(cfg.EmailAttribute, "mail")}
	req := ldap.NewSearchRequest(
		cfg.BaseDN,
		ldap.ScopeWholeSubtree,
		ldap.NeverDerefAliases,
		1,
		0,
		false,
		filter,
		attrs,
		nil,
	)
	res, err := conn.Search(req)
	if err != nil {
		return nil, fmt.Errorf("searching LDAP user: %w", err)
	}
	if len(res.Entries) != 1 {
		return nil, model.ErrInvalidAuth
	}

	entry := res.Entries[0]
	if err := conn.Bind(entry.DN, password); err != nil {
		return nil, model.ErrInvalidAuth
	}

	ldapUsername := entry.GetAttributeValue(firstNonEmpty(cfg.UserNameAttribute, "uid"))
	if ldapUsername == "" {
		ldapUsername = username
	}
	return &ldapUser{
		username: ldapUsername,
		name:     entry.GetAttributeValue(firstNonEmpty(cfg.NameAttribute, "cn")),
		email:    entry.GetAttributeValue(firstNonEmpty(cfg.EmailAttribute, "mail")),
	}, nil
}

func ldapTLSConfig(rawURL string) *tls.Config {
	cfg := &tls.Config{InsecureSkipVerify: conf.Server.LDAP.InsecureSkipVerify} //nolint:gosec
	if parsed, err := url.Parse(rawURL); err == nil {
		cfg.ServerName = parsed.Hostname()
	}
	return cfg
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

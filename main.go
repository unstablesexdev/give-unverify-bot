package main

import (
	"context"
	"errors"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/joho/godotenv"
)

type config struct {
	Token        string
	GuildID      string
	RoleID       string
	AssignOnJoin bool
	Backfill     bool
}

func main() {
	_ = godotenv.Load()

	cfg, err := loadConfig()
	if err != nil {
		log.Fatalf("config error: %v", err)
	}

	dg, err := discordgo.New("Bot " + cfg.Token)
	if err != nil {
		log.Fatalf("discord session error: %v", err)
	}

	dg.Identify.Intents = discordgo.IntentsGuildMembers

	dg.AddHandler(func(s *discordgo.Session, r *discordgo.Ready) {
		log.Printf("bot online as %s#%s", r.User.Username, r.User.Discriminator)
	})

	if cfg.AssignOnJoin {
		dg.AddHandler(func(s *discordgo.Session, m *discordgo.GuildMemberAdd) {
			if m.GuildID != cfg.GuildID || m.User == nil || m.User.Bot {
				return
			}

			if err := addRoleWithRetry(s, cfg.GuildID, m.User.ID, cfg.RoleID); err != nil {
				log.Printf("failed to give role to %s: %v", m.User.ID, err)
				return
			}

			log.Printf("role %s added to new member %s", cfg.RoleID, m.User.ID)
		})
	}

	if err := dg.Open(); err != nil {
		log.Fatalf("failed to connect to discord: %v", err)
	}
	defer dg.Close()

	if cfg.Backfill {
		if err := backfillMembers(dg, cfg); err != nil {
			log.Printf("backfill finished with error: %v", err)
		}
	}

	log.Printf("watching guild %s and role %s", cfg.GuildID, cfg.RoleID)
	log.Printf("assign_on_join=%t backfill=%t", cfg.AssignOnJoin, cfg.Backfill)

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	log.Println("shutdown signal received")
}

func loadConfig() (config, error) {
	cfg := config{
		Token:        resolveToken(),
		GuildID:      strings.TrimSpace(envOrDefault("DISCORD_GUILD_ID", "1489227971561521202")),
		RoleID:       strings.TrimSpace(envOrDefault("DISCORD_ROLE_ID", "1489232986275713146")),
		AssignOnJoin: parseBool(envOrDefault("ASSIGN_ON_JOIN", "true")),
		Backfill:     parseBool(envOrDefault("BACKFILL_EXISTING_MEMBERS", "false")),
	}

	if cfg.Token == "" {
		return config{}, errors.New("DISCORD_BOT_TOKEN is required")
	}
	if cfg.GuildID == "" {
		return config{}, errors.New("DISCORD_GUILD_ID is required")
	}
	if cfg.RoleID == "" {
		return config{}, errors.New("DISCORD_ROLE_ID is required")
	}

	return cfg, nil
}

func resolveToken() string {
	for _, key := range []string{"DISCORD_BOT_TOKEN", "DISCORD_TOKEN", "BOT_TOKEN"} {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value
		}
	}
	return ""
}

func backfillMembers(s *discordgo.Session, cfg config) error {
	log.Printf("starting backfill for guild %s", cfg.GuildID)

	var after string
	totalUpdated := 0

	for {
		members, err := s.GuildMembers(cfg.GuildID, after, 1000)
		if err != nil {
			return err
		}
		if len(members) == 0 {
			break
		}

		for _, member := range members {
			if member == nil || member.User == nil || member.User.Bot {
				continue
			}
			if hasRole(member.Roles, cfg.RoleID) {
				after = member.User.ID
				continue
			}

			if err := addRoleWithRetry(s, cfg.GuildID, member.User.ID, cfg.RoleID); err != nil {
				log.Printf("failed to backfill member %s: %v", member.User.ID, err)
			} else {
				totalUpdated++
				log.Printf("role %s added to existing member %s", cfg.RoleID, member.User.ID)
			}

			after = member.User.ID
			time.Sleep(300 * time.Millisecond)
		}

		if len(members) < 1000 {
			break
		}
	}

	log.Printf("backfill completed, updated %d members", totalUpdated)
	return nil
}

func hasRole(roles []string, roleID string) bool {
	for _, role := range roles {
		if role == roleID {
			return true
		}
	}
	return false
}

func addRoleWithRetry(s *discordgo.Session, guildID, userID, roleID string) error {
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		errCh := make(chan error, 1)

		go func() {
			errCh <- s.GuildMemberRoleAdd(guildID, userID, roleID)
		}()

		select {
		case <-ctx.Done():
			lastErr = ctx.Err()
		case err := <-errCh:
			cancel()
			if err == nil {
				return nil
			}
			lastErr = err
		}

		cancel()
		time.Sleep(time.Duration(attempt+1) * time.Second)
	}

	return lastErr
}

func envOrDefault(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func parseBool(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "y", "on":
		return true
	default:
		return false
	}
}

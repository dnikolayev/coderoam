package app

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/dnikolayev/coderoam/internal/config"
	"github.com/dnikolayev/coderoam/internal/types"
)

const sessionRiskAcceptancePhrase = "I understand"

func (s *cliState) authCommand() *cobra.Command {
	auth := &cobra.Command{Use: "auth", Short: "Manage WhatsApp login"}
	var profile string
	var pairCode string
	var qr bool
	var openQR bool
	var qrImagePath string
	var acceptSessionRisk bool
	login := &cobra.Command{
		Use:   "login",
		Short: "Login with QR code or pairing code",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, path, err := config.LoadOrDefault(s.configPath)
			if err != nil {
				return err
			}
			if profile != "" {
				cfg.App.Profile = profile
			}
			if err := config.EnsureProfileDirs(cfg.App.Profile); err != nil {
				return err
			}
			if err := requireSessionRiskAcknowledgement(cmd, cfg.App.Profile, acceptSessionRisk); err != nil {
				return err
			}
			if _, statErr := os.Stat(path); errors.Is(statErr, os.ErrNotExist) {
				if err := config.Save(path, cfg); err != nil {
					return err
				}
			}
			chatTransport, err := s.buildTransport(cmd.Context(), cfg)
			if err != nil {
				return err
			}
			defer chatTransport.Close(context.Background())
			return chatTransport.Login(cmd.Context(), types.LoginMethod{
				QR:            qr || pairCode == "",
				PairCodePhone: pairCode,
				QRImagePath:   qrImagePath,
				OpenQRImage:   openQR,
			})
		},
	}
	login.Flags().StringVar(&profile, "profile", "", "profile name")
	login.Flags().BoolVar(&qr, "qr", true, "login with terminal QR code")
	login.Flags().StringVar(&pairCode, "pair-code", "", "login with pairing code for this phone number")
	login.Flags().BoolVar(&openQR, "open-qr", true, "open generated QR image with the system image viewer")
	login.Flags().StringVar(&qrImagePath, "qr-image", "", "path for generated QR PNG")
	login.Flags().BoolVar(&acceptSessionRisk, "accept-session-risk", false, "acknowledge unofficial transport and local session-storage risk without an interactive prompt")

	status := &cobra.Command{
		Use:   "status",
		Short: "Show WhatsApp auth status",
		RunE: func(cmd *cobra.Command, args []string) error {
			return s.printStatus(cmd.Context())
		},
	}
	logout := &cobra.Command{
		Use:   "logout",
		Short: "Logout and invalidate local WhatsApp session",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, _, err := s.loadConfig()
			if err != nil {
				return err
			}
			chatTransport, err := s.buildTransport(cmd.Context(), cfg)
			if err != nil {
				return err
			}
			defer chatTransport.Close(context.Background())
			if err := chatTransport.Logout(cmd.Context()); err != nil {
				return err
			}
			fmt.Println("logged out")
			return nil
		},
	}
	var resetYes bool
	reset := &cobra.Command{
		Use:   "reset",
		Short: "Delete local WhatsApp session files after confirmation",
		RunE: func(cmd *cobra.Command, args []string) error {
			if !resetYes {
				return fmt.Errorf("refusing to delete WhatsApp session files without --yes")
			}
			cfg, _, err := s.loadConfig()
			if err != nil {
				return err
			}
			paths := sessionFilePaths(cfg.App.Profile)
			for _, path := range paths {
				if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
					return err
				}
			}
			fmt.Printf("deleted WhatsApp session files for profile=%s\n", cfg.App.Profile)
			return nil
		},
	}
	reset.Flags().BoolVar(&resetYes, "yes", false, "confirm deletion of local WhatsApp session files")
	auth.AddCommand(login, status, logout, reset)
	return auth
}

func sessionFilePaths(profile string) []string {
	base := config.SessionStorePath(profile)
	return []string{
		base,
		base + "-shm",
		base + "-wal",
		base + ".qr.png",
	}
}

func requireSessionRiskAcknowledgement(cmd *cobra.Command, profile string, accepted bool) error {
	return requireSessionRiskAcknowledgementWithReader(cmd, profile, accepted, nil)
}

func requireSessionRiskAcknowledgementWithReader(cmd *cobra.Command, profile string, accepted bool, reader *bufio.Reader) error {
	needed, err := sessionRiskAcknowledgementNeeded(profile)
	if err != nil {
		return err
	}
	if !needed {
		return nil
	}
	if accepted {
		return nil
	}
	stderr := cmd.ErrOrStderr()
	fmt.Fprintln(stderr, "Before first WhatsApp login, acknowledge this session risk:")
	fmt.Fprintln(stderr, "- The WhatsApp Web transport is unofficial and can break or risk account restrictions.")
	fmt.Fprintf(stderr, "- Local session material will be stored at %s and is sensitive.\n", config.SessionStorePath(profile))
	fmt.Fprintln(stderr, "- Use a dedicated WhatsApp account and keep usage low-volume.")
	if !interactiveReader(cmd.InOrStdin()) {
		return fmt.Errorf("first WhatsApp login requires session-risk acknowledgement; rerun with --accept-session-risk after reading SECURITY.md")
	}
	fmt.Fprintf(stderr, "Type %q to continue: ", sessionRiskAcceptancePhrase)
	if reader == nil {
		reader = bufio.NewReader(cmd.InOrStdin())
	}
	line, readErr := reader.ReadString('\n')
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		return readErr
	}
	if strings.TrimSpace(line) != sessionRiskAcceptancePhrase {
		return fmt.Errorf("session-risk acknowledgement not accepted")
	}
	return nil
}

func sessionRiskAcknowledgementNeeded(profile string) (bool, error) {
	if _, err := os.Stat(config.SessionStorePath(profile)); err == nil {
		return false, nil
	} else if errors.Is(err, os.ErrNotExist) {
		return true, nil
	} else {
		return false, err
	}
}

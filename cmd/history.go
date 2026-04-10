package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"fishnet/internal/interaction"
)

// historyCmd manages past interaction sessions (interviews, surveys, report chats).
var historyCmd = &cobra.Command{
	Use:   "history",
	Short: "View past interaction sessions (interviews, surveys, report chats)",
}

var historyListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all past interaction sessions",
	Example: `  fishnet history list
  fishnet history list --type survey`,
	RunE: func(cmd *cobra.Command, args []string) error {
		typeFilter, _ := cmd.Flags().GetString("type")

		mgr := interaction.NewManager(".")
		sessions, err := mgr.List()
		if err != nil {
			return err
		}
		if len(sessions) == 0 {
			fmt.Println("No interaction sessions found.")
			fmt.Println("  Run: fishnet interview <agent>  -- to interview an agent")
			fmt.Println("  Run: fishnet survey --question \"...\"  -- to survey all agents")
			fmt.Println("  Run: fishnet report chat  -- to chat with the report agent")
			return nil
		}

		var filtered []*interaction.Session
		for _, s := range sessions {
			if typeFilter != "" && s.Type != typeFilter {
				continue
			}
			filtered = append(filtered, s)
		}
		if len(filtered) == 0 {
			fmt.Printf("No %s sessions found.\n", typeFilter)
			return nil
		}

		fmt.Printf("%-28s  %-12s  %-5s  %-5s  %s\n",
			"ID", "TYPE", "TURNS", "RESP", "QUESTION/AGENT")
		fmt.Println(strings.Repeat("-", 85))
		for _, s := range filtered {
			turns := len(s.History) / 2
			answers := len(s.Answers)
			subject := s.Question
			if subject == "" {
				subject = s.AgentName
			}
			if subject == "" {
				subject = s.ReportID
			}
			if len([]rune(subject)) > 35 {
				subject = string([]rune(subject)[:34]) + "…"
			}
			fmt.Printf("%-28s  %-12s  %5d  %5d  %s\n",
				s.ID, s.Type, turns, answers, subject)
		}
		return nil
	},
}

var historyShowCmd = &cobra.Command{
	Use:   "show <session-id>",
	Short: "Show the full contents of an interaction session",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		mgr := interaction.NewManager(".")
		s, err := mgr.Load(args[0])
		if err != nil {
			return err
		}

		fmt.Printf("\n%s  %s\n", bold("Session:"), s.ID)
		fmt.Printf("  Type:     %s\n", s.Type)
		fmt.Printf("  Created:  %s\n", s.CreatedAt.Format("2006-01-02 15:04:05"))
		if s.AgentName != "" {
			fmt.Printf("  Agent:    %s\n", s.AgentName)
		}
		if s.Question != "" {
			fmt.Printf("  Question: %s\n", s.Question)
		}
		if s.ReportID != "" {
			fmt.Printf("  Report:   %s\n", s.ReportID)
		}

		// Survey answers
		if len(s.Answers) > 0 {
			fmt.Printf("\n%s Survey Responses (%d agents):\n\n", bold("→"), len(s.Answers))
			for _, a := range s.Answers {
				fmt.Printf("  %s (%s)\n", cyan(a.AgentName), a.AgentType)
				fmt.Printf("  %s\n\n", a.Response)
			}
		}

		// Conversation history
		if len(s.History) > 0 {
			fmt.Printf("\n%s Conversation (%d turns):\n\n", bold("→"), len(s.History)/2)
			for _, msg := range s.History {
				switch msg.Role {
				case "user":
					fmt.Printf("  %s: %s\n\n", bold("you"), msg.Content)
				case "assistant":
					name := s.AgentName
					if name == "" {
						name = "agent"
					}
					fmt.Printf("  %s: %s\n\n", cyan(name), msg.Content)
				}
			}
		}

		return nil
	},
}

func init() {
	historyListCmd.Flags().String("type", "", "Filter by type: interview|survey|report_chat")
	historyCmd.AddCommand(historyListCmd)
	historyCmd.AddCommand(historyShowCmd)
	rootCmd.AddCommand(historyCmd)
}

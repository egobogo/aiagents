// internal/agent/backend.go
package agent

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/egobogo/aiagents/internal/trello"
)

// BackendDeveloperAIAgent specializes in generating code.
type BackendDeveloperAIAgent struct {
	*AIAgent // embed base agent
}

// NewBackendDeveloperAIAgent creates a new backend developer agent.
func NewBackendDeveloperAIAgent(base *AIAgent) *BackendDeveloperAIAgent {
	return &BackendDeveloperAIAgent{
		AIAgent: base,
	}
}

// GenerateCode generates Go code based on a specific task.
func (b *BackendDeveloperAIAgent) GenerateCode(task string) (string, error) {
	// Combine the specialized instruction with the task.
	prompt := fmt.Sprintf("Task: %s", task)
	return b.GPTClient.Chat(prompt)
}

// RequestDirectClarification posts a clarification request on the ticket and directly
// obtains a clarification response from the Engineering Manager agent.
func (b *BackendDeveloperAIAgent) RequestDirectClarification(ticket *trello.Card, manager *EngineeringManagerAIAgent) error {
	//Request clarification from GPT
	prompt := fmt.Sprintf("Carefully study a technical assigment prepared by the engineering manager and ask clarifications if anything is not clear to you. Be precise in your questions, concrete, professional, don't ask obvious questions.:\n%s", ticket.Desc)
	clarificationRequest, err := b.GPTClient.Chat(prompt)
	if err != nil {
		return fmt.Errorf("failed to generate code: %w", err)
	}

	// Construct and post the clarification request comment.
	requestComment := fmt.Sprintf("Backend Developer Request: %s @%s", clarificationRequest, manager.Name)
	if err := b.WriteComment(ticket, requestComment); err != nil {
		return fmt.Errorf("failed to post clarification request: %w", err)
	}

	// Directly call the manager's method to generate a clarification response.
	response, err := manager.RespondToClarification(ticket, clarificationRequest, b)
	if err != nil {
		return fmt.Errorf("failed to obtain clarification response: %w", err)
	}

	log.Printf("Received clarification response: %s", response)
	return nil
}

// ExecuteTechnicalAssignment generates production-ready Go code based on the ticket's description,
// expecting GPT to output the code in a strict format:
//   - The first line is the full file path (including folder structure) wrapped in double exclamation marks
//     (e.g. !!internal/agent/backend.go!!).
//   - All subsequent lines contain only the Go code and comments.
//
// The function creates the folder structure if it does not already exist.
func (b *BackendDeveloperAIAgent) ExecuteTechnicalAssignment(ticket *trello.Card) (string, error) {
	// Create a prompt instructing GPT to follow the strict format.
	prompt := fmt.Sprintf(
		"Generate production-ready Go code with tests for the following technical assignment:\n%s\n"+
			"It is crucial that the output strictly follows this format:\n"+
			"1. The first line must be the full file path (including folder structure) wrapped in double exclamation marks (e.g. !!internal/agent/backend.go!!).\n"+
			"2. All subsequent lines must be the Go code and comments only, with no additional text or explanations.",
		ticket.Desc,
	)

	// Get the response from GPT.
	response, err := b.GPTClient.Chat(prompt)
	if err != nil {
		return "", fmt.Errorf("failed to generate code: %w", err)
	}

	// Split the response into lines.
	lines := strings.Split(response, "\n")
	if len(lines) < 2 {
		return "", fmt.Errorf("GPT response is incomplete, expected at least a file path and code content")
	}

	// Validate and extract the file path from the first line.
	filenameLine := strings.TrimSpace(lines[0])
	if !strings.HasPrefix(filenameLine, "!!") || !strings.HasSuffix(filenameLine, "!!") {
		return "", fmt.Errorf("invalid file path format in GPT response: %s", filenameLine)
	}
	// Remove the wrapping exclamation marks to extract the file path.
	filePath := strings.Trim(filenameLine, "!")

	// Ensure the target directory exists.
	dir := filepath.Dir(filePath)
	if dir != "." {
		fullDirPath := filepath.Join(b.GitClient.RepoPath, dir)
		if err := os.MkdirAll(fullDirPath, os.ModePerm); err != nil {
			return "", fmt.Errorf("failed to create directory %s: %w", fullDirPath, err)
		}
	}

	// Combine the remaining lines as the code content.
	rawCodeContent := strings.Join(lines[1:], "\n")
	codeContent := cleanCodeOutput(rawCodeContent)

	// Write the generated code to the Git repository using the full file path.
	if err := b.WriteToGit(filePath, []byte(codeContent)); err != nil {
		return "", fmt.Errorf("failed to write generated code to git: %w", err)
	}

	// Optionally, generate a commit message summarizing the changes.
	commitPrompt := "Summarize the changes made for this commit in a concise message."
	commitMessage, err := b.GPTClient.Chat(commitPrompt)
	if err != nil {
		return "", fmt.Errorf("failed to generate commit message: %w", err)
	}

	return commitMessage, nil
}

// cleanCodeOutput removes any markdown formatting (like triple backticks)
// from the GPT-generated code.
func cleanCodeOutput(code string) string {
	// Remove leading and trailing whitespace.
	code = strings.TrimSpace(code)

	// Remove starting triple backticks and optional language specifier.
	if strings.HasPrefix(code, "```") {
		// Find the first newline after the opening backticks.
		idx := strings.Index(code, "\n")
		if idx != -1 {
			code = code[idx+1:]
		}
	}

	// Remove trailing triple backticks.
	if strings.HasSuffix(code, "```") {
		code = strings.TrimSuffix(code, "```")
	}

	return strings.TrimSpace(code)
}

// CommitAndPushTicketResult commits changes and pushes them to the remote repository.
func (b *BackendDeveloperAIAgent) CommitAndPushTicketResult(ticket *trello.Card, commitMessage, authorName, authorEmail, gitUsername, gitToken string) error {
	fullMessage := fmt.Sprintf("%s (Ticket: %s)", commitMessage, ticket.Name)
	if err := b.GitClient.CommitChanges(fullMessage, authorName, authorEmail); err != nil {
		return fmt.Errorf("failed to commit changes: %w", err)
	}

	// Pull changes to update local branch.
	if err := b.GitClient.PullChanges(); err != nil {
		log.Printf("Warning: pull failed, proceeding to push might result in a non-fast-forward error: %v", err)
	}

	if err := b.GitClient.PushChanges(gitUsername, gitToken); err != nil {
		return fmt.Errorf("failed to push changes: %w", err)
	}
	return nil
}

// CloseTicket moves the ticket to the Done column and reassigns it.
func (b *BackendDeveloperAIAgent) CloseTicket(ticket *trello.Card, finalAssignee string) error {
	// Get the ID for the Done column. (Assume TrelloClient has GetDoneListID.)
	doneListID, err := b.TrelloClient.GetListIDByName("Done")
	if err != nil {
		return fmt.Errorf("failed to get Done list ID: %w", err)
	}
	if err := b.ChangeTicketColumn(ticket, doneListID); err != nil {
		return fmt.Errorf("failed to move ticket to Done: %w", err)
	}
	if err := b.ChangeTicketAssignee(ticket, finalAssignee); err != nil {
		return fmt.Errorf("failed to reassign ticket: %w", err)
	}
	return nil
}

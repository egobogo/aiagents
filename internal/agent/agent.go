// internal/agent/agent.go
package agent

import (
	"fmt"
	"log"
	"path/filepath"
	"strings"

	"github.com/egobogo/aiagents/internal/gitrepo"
	chatgpt "github.com/egobogo/aiagents/internal/model"
	"github.com/egobogo/aiagents/internal/trello"
)

// AIAgent is the base type that provides common functionality for all agents.
type AIAgent struct {
	Name         string
	TrelloClient *trello.TrelloClient
	GitClient    *gitrepo.GitClient
	GPTClient    *chatgpt.ChatGPTClient
	Purpose      string
}

// NewBaseAgent initializes and returns a new AIAgent.
// It sets the agent's purpose by sending a system message to the GPT client,
// and it loads the project guidance from the "IMPORTANT" Trello column.
func NewBaseAgent(name string, trelloClient *trello.TrelloClient, gitClient *gitrepo.GitClient, gptClient *chatgpt.ChatGPTClient, purpose string) (*AIAgent, error) {
	agent := &AIAgent{
		Name:         name,
		TrelloClient: trelloClient,
		GitClient:    gitClient,
		GPTClient:    gptClient,
		Purpose:      purpose,
	}

	// Load project guidance tickets into GPT context.
	if err := agent.LoadGuidanceTickets(); err != nil {
		log.Printf("Warning: Failed to load guidance tickets for agent %s: %v", name, err)
		// Optionally, you might not want to fail the initialization if guidance is missing.
	}

	// Send the purpose to GPT as a system message.
	messages := []chatgpt.Message{
		{Role: "system", Content: purpose},
	}
	_, err := agent.GPTClient.ChatWithMessages(messages)
	if err != nil {
		log.Printf("Failed to set purpose for agent %s: %v", name, err)
		// Depending on your policy, you may choose to return an error or continue.
		return nil, fmt.Errorf("failed to set purpose: %w", err)
	}
	log.Printf("Agent %s initialized with purpose: %s", name, purpose)
	return agent, nil
}

// TicketBelongsToMe checks if a Trello card is assigned to this agent.
// (This is a simplistic check comparing agent's Name with each assigned member ID.)
func (a *AIAgent) TicketBelongsToMe(card *trello.Card) bool {
	// For each memberID on the card, fetch the member details.
	for _, memberID := range card.IDMembers {
		member, err := a.TrelloClient.GetMember(memberID)
		if err != nil {
			continue // or log the error
		}
		// Compare the member's full name or username with the agent's name.
		if strings.EqualFold(member.Username, a.Name) {
			return true
		}
	}
	return false
}

func (a *AIAgent) ChangeTicketColumn(card *trello.Card, newListID string) error {
	myCard := trello.WrapCard(card)
	return myCard.Move(newListID)
}

// ChangeTicketAssignee updates a card's assignee.
func (a *AIAgent) ChangeTicketAssignee(card *trello.Card, newAssignee string) error {
	myCard := trello.WrapCard(card) // Wrap the card to get access to helper methods.
	return myCard.AssignMember(newAssignee)
}

// WriteComment adds a comment to a card.
func (a *AIAgent) WriteComment(card *trello.Card, comment string) error {
	myCard := trello.WrapCard(card)
	// Use PostComment instead of AddComment to avoid the conflict.
	return myCard.PostComment(comment, a.TrelloClient)
}

func (a *AIAgent) ReadComments(card *trello.Card) ([]string, error) {
	myCard := trello.WrapCard(card)
	return myCard.GetComments(a.TrelloClient)
}

// GetTaggedComments returns comments that mention the agent (e.g. "@AgentName").
func (a *AIAgent) GetTaggedComments(card *trello.Card) ([]string, error) {
	comments, err := a.ReadComments(card)
	if err != nil {
		return nil, err
	}
	var tagged []string
	tag := "@" + a.Name
	for _, c := range comments {
		if strings.Contains(c, tag) {
			tagged = append(tagged, c)
		}
	}
	return tagged, nil
}

// ReadAllGitFiles delegates to the Git client to read the repository files.
func (a *AIAgent) ReadAllGitFiles() (map[string]string, error) {
	return a.GitClient.ReadAllFiles()
}

// WriteToGit writes content to a file in the repository via the Git client.
func (a *AIAgent) WriteToGit(fileName string, content []byte) error {
	return a.GitClient.WriteFile(fileName, content)
}

// StartRoutine reminds the agent of its goal and observes the Git repository.
func (a *AIAgent) StartRoutine() error {
	files, err := a.ReadAllGitFiles()
	if err != nil {
		return err
	}
	for path := range files {
		log.Printf("Found file: %s", path)
	}
	return nil
}

// GetAssignedTickets fetches all Trello cards on the board and returns those assigned to the agent.
func (a *AIAgent) GetAssignedTickets() ([]*trello.Card, error) {
	board, err := a.TrelloClient.GetBoard()
	if err != nil {
		return nil, fmt.Errorf("failed to get board: %w", err)
	}
	// Retrieve all cards; passing nil for options.
	cards, err := board.GetCards(nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get cards: %w", err)
	}
	var assigned []*trello.Card
	var needToRefreshContext = true
	for _, card := range cards {
		if a.TicketBelongsToMe(card) {
			assigned = append(assigned, card)
			if needToRefreshContext {
				refreshErr := a.RefreshProjectContext()
				if refreshErr != nil {
					return nil, fmt.Errorf("failed to refresh context: %w", refreshErr)
				}
				needToRefreshContext = false
			}
		}
	}
	needToRefreshContext = true
	return assigned, nil
}

// RefreshProjectContext reads the project’s folder structure and full file contents from Git,
// and sends this information to the GPT agent to update its internal context without expecting a response.
func (a *AIAgent) RefreshProjectContext() error {
	// 1. Read all files from the Git repository.
	files, err := a.ReadAllGitFiles()
	if err != nil {
		return fmt.Errorf("failed to read git files: %w", err)
	}

	// 2. Build a summary including folder structure, file locations, and full file contents.
	var summaryBuilder strings.Builder
	summaryBuilder.WriteString("Project Context Update:\n")
	summaryBuilder.WriteString("The following is the project folder structure along with file locations and full file contents:\n\n")

	for filePath, content := range files {
		dir := filepath.Dir(filePath)
		summaryBuilder.WriteString(fmt.Sprintf("File: %s\n", filePath))
		summaryBuilder.WriteString(fmt.Sprintf("Location: %s\n", dir))
		summaryBuilder.WriteString("Content:\n")
		summaryBuilder.WriteString(content)
		summaryBuilder.WriteString("\n----------------\n")
	}

	// 3. Create a prompt that instructs GPT to update its internal context without generating a response.
	prompt := fmt.Sprintf(
		"%s\n\nNote: This information is provided solely to actualise and update your internal understanding of the project structure and code base. No response or commentary is needed.",
		summaryBuilder.String(),
	)

	// 4. Send the prompt to the GPT agent.
	_, err = a.GPTClient.Chat(prompt)
	if err != nil {
		return fmt.Errorf("failed to update GPT context: %w", err)
	}

	return nil
}

// LoadGuidanceTickets retrieves all cards from the "IMPORTANT" list,
// aggregates their titles and descriptions, and sends them to the GPT client as a system message.
func (a *AIAgent) LoadGuidanceTickets() error {
	// Get the list ID for the IMPORTANT column.
	listID, err := a.TrelloClient.GetListIDByName("IMPORTANT")
	if err != nil {
		return fmt.Errorf("failed to get list ID for IMPORTANT: %w", err)
	}

	// Retrieve the board.
	board, err := a.TrelloClient.GetBoard()
	if err != nil {
		return fmt.Errorf("failed to get board: %w", err)
	}

	// Retrieve all cards from the board.
	cards, err := board.GetCards(nil)
	if err != nil {
		return fmt.Errorf("failed to get cards from board: %w", err)
	}

	// Aggregate guidance text from cards in the IMPORTANT list.
	var guidanceBuilder strings.Builder
	guidanceBuilder.WriteString("Guidance Tickets:\n")
	for _, card := range cards {
		if card.IDList == listID {
			guidanceBuilder.WriteString(fmt.Sprintf("Title: %s\n", card.Name))
			guidanceBuilder.WriteString(fmt.Sprintf("Details: %s\n\n", card.Desc))
		}
	}
	guidanceText := guidanceBuilder.String()
	if guidanceText == "Guidance Tickets:\n" {
		// No guidance tickets found.
		return nil
	}

	// Send the aggregated guidance as a system message to GPT.
	messages := []chatgpt.Message{
		{Role: "system", Content: guidanceText},
	}
	// We don't use the response; this call is meant to update GPT's context.
	_, err = a.GPTClient.ChatWithMessages(messages)
	if err != nil {
		return fmt.Errorf("failed to update GPT guidance context: %w", err)
	}

	log.Printf("Guidance tickets sent to GPT client.")
	return nil
}

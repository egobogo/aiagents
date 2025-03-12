// internal/agent/engmanager.go
package agent

import (
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/egobogo/aiagents/internal/trello"
)

// EngineeringManagerAIAgent specializes in ticket analysis and task decomposition.
type EngineeringManagerAIAgent struct {
	*AIAgent
}

// AssignTicketToAgent assigns the given ticket (card) to the specified agent by updating its member assignment.
func (e *EngineeringManagerAIAgent) AssignTicketToAgent(card *trello.Card, agentName string) error {
	// Wrap the card to get access to our helper methods.
	myCard := trello.WrapCard(card)
	// Call our helper method to update the assignment.
	member, err := e.TrelloClient.GetMemberByName(agentName)
	if member == nil {
		return fmt.Errorf("failed to find an agent %s: %w", agentName, err)
	}

	if err := myCard.AssignMember(member.ID); err != nil {
		return fmt.Errorf("failed to assign ticket to agent %s: %w", agentName, err)
	}
	return nil
}

// NewEngineeringManagerAIAgent creates a new engineering manager agent.
func NewEngineeringManagerAIAgent(base *AIAgent) *EngineeringManagerAIAgent {
	return &EngineeringManagerAIAgent{
		AIAgent: base,
	}
}

// HandleTicket processes a ticket by generating clarifications based on Git context and ticket details,
// posting them (tagging @bogoego), waiting for a reply that tags @egobogoengmanageragent,
// and finally passing that reply to GPT.
func (e *EngineeringManagerAIAgent) HandleTicket(card *trello.Card) ([]*trello.Card, error) {
	// 2. Pass the ticket details and git context to GPT to generate clarifications.
	ticketInfo := fmt.Sprintf("Ticket ID: %s\nTitle: %s\nDescription: %s", card.ID, card.Name, card.Desc)
	prompt := fmt.Sprintf(
		"Given the following ticket details:%s\n"+
			"You are a highly skilled AI Engineering Manager agent. Your role is to confirm that the business requirements of this ticket are clear. You already know the best technical approaches, including libraries, design patterns, testing frameworks, coding standards, and all other technical details. Do NOT ask any questions about these technical matters.\n"+
			"Do NOT ask about:\n"+
			"- Commenting guidelines or documentation standards.\n"+
			"- Performance benchmarks or optimizations.\n"+
			"- Review processes, stakeholder impacts, or timelines.\n"+
			"- Any technical details regarding libraries, design patterns, or testing frameworks.\n"+
			"- Any summaries, headers, footers, unrelated comments.\n"+
			"Only ask clarifying questions if there is any ambiguity in the business requirements. Do not ask about commenting guidelines, performance, review processes, stakeholder impacts, timelines, or any technical specifics.\n"+
			"Ask concise questions about any missing business objectives. Make sure you really understand what the fuction is about to be ready to write technical tickets.", ticketInfo)
	clarifications, err := e.GPTClient.Chat(prompt)
	if err != nil {
		return nil, fmt.Errorf("failed to generate clarifications: %w", err)
	}

	// 3. Post the generated clarifications as a comment, tagging @bogoego.
	clarificationComment := clarifications + "\n@bogoego"
	if err := e.WriteComment(card, clarificationComment); err != nil {
		return nil, fmt.Errorf("failed to post clarification comment: %w", err)
	}
	log.Printf("Posted clarifications on ticket %s", card.ID)

	// 4. Wait until a reply is posted that tags @egobogoengmanageragent.
	reply, err := e.WaitForReply(card, "@"+e.Name)
	if err != nil {
		return nil, fmt.Errorf("failed to receive reply: %w", err)
	}
	log.Printf("Received reply: %s", reply)

	// 5. Pass the reply to GPT for further processing.
	replyPrompt := fmt.Sprintf(
		"Given the following clarifications, create tenchnical clear a list of atomic technical tickets for the backend developer with only coding tasks. Each task should be clear and unambiguous, with a concise title. Each task should start with a title followed by new line and then have a precise technical specification for the developer.  I want the response to ONLY have actionable tickets withno additional fields, no general questins or comments, compact, precise. Each ticket should be separated one from eachother by \n@@@@\n")

	response, err := e.GPTClient.Chat(reply + "\n" + replyPrompt)
	if err != nil {
		return nil, fmt.Errorf("failed to process reply with GPT: %w", err)
	}

	// After receiving the response from GPT that contains the tasks:
	tasksParsed, err := parseTasksFromResponse(response)
	if err != nil {
		log.Printf("Error parsing tasks: %v", err)
		return nil, fmt.Errorf("failed to parse tasks: %w", err)
	}

	var createdTickets []*trello.Card

	// Get the list ID for the "Doing" column.
	doingListID, err := e.TrelloClient.GetListIDByName("Doing")
	if err != nil {
		return nil, fmt.Errorf("failed to get Doing list ID: %w", err)
	}

	// Iterate through the parsed tasks and create a technical ticket for each.
	for _, task := range tasksParsed {
		// Create the ticket using the title and description.
		techTicket, err := e.TrelloClient.CreateCard(task.Title, task.Description, doingListID)
		if err != nil {
			log.Printf("failed to create technical ticket for task '%s': %v", task.Title, err)
			continue
		}
		createdTickets = append(createdTickets, techTicket)
	}
	return createdTickets, nil
}

// parseTasksFromResponse takes the GPT response and extracts tasks.
// It splits the response on "\n@@@@\n" so that each block represents a task.
// The first line of each block is taken as the title and the rest as the description.
func parseTasksFromResponse(response string) ([]struct{ Title, Description string }, error) {
	var tasks []struct{ Title, Description string }

	// Split the response by the delimiter that separates tasks.
	taskBlocks := strings.Split(response, "\n@@@@\n")
	for _, block := range taskBlocks {
		block = strings.TrimSpace(block)
		if block == "" {
			continue
		}

		// Split the block into lines.
		lines := strings.Split(block, "\n")
		if len(lines) == 0 {
			continue // Skip if no content is present.
		}

		// The first line is the title.
		title := strings.TrimSpace(lines[0])
		// The remaining lines (if any) are the description.
		description := ""
		if len(lines) > 1 {
			description = strings.TrimSpace(strings.Join(lines[1:], "\n"))
		}

		tasks = append(tasks, struct{ Title, Description string }{
			Title:       title,
			Description: description,
		})
	}

	return tasks, nil
}

// WaitForReply polls the ticket's comments until one is found that contains the required tag.
func (e *EngineeringManagerAIAgent) WaitForReply(card *trello.Card, requiredTag string) (string, error) {
	const pollInterval = 60 * time.Second
	const maxAttempts = 100 // Adjust as needed

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		comments, err := e.ReadComments(card) // defined in the base agent
		if err != nil {
			return "", fmt.Errorf("failed to read comments: %w", err)
		}
		for _, comment := range comments {
			if strings.Contains(comment, requiredTag) {
				return comment, nil
			}
		}
		log.Printf("No reply with %s found, attempt %d/%d. Waiting...", requiredTag, attempt, maxAttempts)
		time.Sleep(pollInterval)
	}
	return "", fmt.Errorf("reply with tag %s not received after polling", requiredTag)
}

// RespondToClarification generates a clarification response using ChatGPT and posts it as a comment.
func (e *EngineeringManagerAIAgent) RespondToClarification(ticket *trello.Card, clarificationRequest string, backend *BackendDeveloperAIAgent) (string, error) {
	// Build a prompt that includes the agent's instruction and the clarification request.
	prompt := fmt.Sprintf("Provide a detailed clarification for the following request from the developer agent. Always answer questions, don't ask them. Remember - your vision is the source of all engineering truth of the project. Here is the question from the engineer: %s", clarificationRequest)

	clarification, err := e.GPTClient.Chat(prompt)
	if err != nil {
		return "", fmt.Errorf("failed to generate clarification: %w", err)
	}

	// Construct the response comment tagging the backend agent.
	responseComment := fmt.Sprintf("Response: %s @%s", clarification, backend.Name)
	if err := e.WriteComment(ticket, responseComment); err != nil {
		return "", fmt.Errorf("failed to post clarification response: %w", err)
	}

	return clarification, nil
}

// CreateTechnicalTicket generates a technical ticket based on a high-level ticket.
func (e *EngineeringManagerAIAgent) CreateTechnicalTicket(ticket *trello.Card) (*trello.Card, error) {
	prompt := fmt.Sprintf("Decompose this ticket into detailed technical tasks with clear atomic assignments:\n%s", ticket.Desc)
	techDesc, err := e.GPTClient.Chat(prompt)
	if err != nil {
		return nil, fmt.Errorf("failed to generate technical description: %w", err)
	}
	// Create a new card with a technical header.
	// In the Engineering Manager agent when creating a technical ticket:
	doingListID, err := e.TrelloClient.GetListIDByName("Doing")
	if err != nil {
		return nil, fmt.Errorf("failed to get Doing list ID: %w", err)
	}
	techTicket, err := e.TrelloClient.CreateCard("Technical: "+ticket.Name, techDesc, doingListID)
	if err != nil {
		return nil, fmt.Errorf("failed to create technical ticket: %w", err)
	}
	return techTicket, nil
}

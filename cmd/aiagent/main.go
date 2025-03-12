// cmd/ai_agent/main.go
package main

import (
	"log"
	"os"
	"time"

	"github.com/egobogo/aiagents/internal/agent"
	"github.com/egobogo/aiagents/internal/gitrepo"
	chatgpt "github.com/egobogo/aiagents/internal/model"
	"github.com/egobogo/aiagents/internal/roles"
	"github.com/egobogo/aiagents/internal/trello"
	"github.com/joho/godotenv"
)

func main() {
	// Load environment variables from .env file.
	log.Println("Fetching env")
	if err := godotenv.Load(); err != nil {
		log.Println("No .env file found; using system environment variables")
	}

	// Initialize core clients.
	log.Println("creating clients")
	trelloClient := trello.NewTrelloClient(os.Getenv("TRELLO_API_KEY"), os.Getenv("TRELLO_API_TOKEN"), os.Getenv("TRELLO_BOARD_ID"))
	gitClient, err := gitrepo.NewGitClient(os.Getenv("GIT_REPO_URL"), os.Getenv("GIT_REPO_PATH"))
	if err != nil {
		log.Fatalf("Error creating Git client: %v", err)
	}
	// Initialize ChatGPT client using the chosen model.
	gptClient := chatgpt.NewChatGPTClient("gpt-4o-mini")

	// egobogoBackendDevAgent@gmail.com
	baseEngAgent, err := agent.NewBaseAgent("egobogoengmanageragent", trelloClient, gitClient, gptClient, roles.Manager.SystemMessage)
	if err != nil {
		log.Fatalf("Failed to initialize Engineering Manager base agent: %v", err)
	}

	baseDevAgent, err := agent.NewBaseAgent("egobogobackenddevagent", trelloClient, gitClient, gptClient, roles.Backend.SystemMessage)
	if err != nil {
		log.Fatalf("Failed to initialize Backend Developer base agent: %v", err)
	}

	// Create specialized agents using predefined role configurations.
	engManagerAgent := agent.NewEngineeringManagerAIAgent(baseEngAgent)
	backendAgent := agent.NewBackendDeveloperAIAgent(baseDevAgent)

	// Main event loop: poll for new tickets and process them.
	log.Println("starting main loop")
	for {
		// 1. Engineering Manager polls Trello for tickets assigned to him.
		tickets, err := engManagerAgent.GetAssignedTickets() // (Method to fetch tickets assigned to "engManagerAgent")
		if err != nil {
			log.Printf("Error fetching assigned tickets: %v", err)
		}

		if len(tickets) > 0 {
			for _, ticket := range tickets {
				// Engineering Manager reads the ticket and writes comments if needed,
				// waiting for manual approval or clarification via Trello comments.
				techTickets, err := engManagerAgent.HandleTicket(ticket)
				if err != nil {
					log.Printf("Error handling ticket %s: %v", ticket.ID, err)
					continue
				}

				for _, techTicket := range techTickets {
					// Assign the technical ticket to the Backend Developer agent.
					if err := engManagerAgent.AssignTicketToAgent(techTicket, backendAgent.Name); err != nil {
						log.Printf("Error assigning technical ticket %s to developer: %v", techTicket.ID, err)
					}
				}
			}
		} else {
			log.Println("No tickets assigned to eng manager found")
		}

		// 2. Backend Developer polls for technical tickets assigned to it.
		techTickets, err := backendAgent.GetAssignedTickets() // (Method to fetch tickets assigned to backend agent)
		if err != nil {
			log.Printf("Error fetching technical tickets: %v", err)
		}

		if len(techTickets) > 0 {
			gitUsername := os.Getenv("GIT_USERNAME")
			gitToken := os.Getenv("GIT_TOKEN")

			for _, tkt := range techTickets {
				// Backend Developer asks for a clarification from the Engineering Manager.
				err := backendAgent.RequestDirectClarification(tkt, engManagerAgent)
				if err != nil {
					log.Printf("Error in direct clarification: %v", err)
				}

				// Execute the technical assignment: generate code, write tests.
				comment, err := backendAgent.ExecuteTechnicalAssignment(tkt)
				if err != nil {
					log.Printf("Error executing technical assignment for ticket %s: %v", tkt.ID, err)
					continue
				}
				// Commit the result to Git.
				err = backendAgent.CommitAndPushTicketResult(tkt,
					comment,             // commit message
					backendAgent.Name,   // author name
					"egobogo@gmail.com", // author email
					gitUsername,         // git username (from env or config)
					gitToken)
				if err != nil {
					log.Printf("Error committing result for ticket %s: %v", tkt.ID, err)
					continue
				}
				// Finally, mark the ticket as done and reassign it (for example, to "me").
				if err := backendAgent.CloseTicket(tkt, "engManagerAgent"); err != nil {
					log.Printf("Error closing ticket %s: %v", tkt.ID, err)
				}
			}
		}

		// Sleep before polling again.
		time.Sleep(30 * time.Second)
	}
}

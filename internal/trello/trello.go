// internal/trello/trello.go
package trello

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"

	adlio "github.com/adlio/trello"
)

// For convenience, alias the adlio.Card type.
type Card = adlio.Card

func WrapCard(card *Card) *MyCard {
	return &MyCard{Card: card}
}

// MyCard wraps *Card to add helper methods.
type MyCard struct {
	*Card
}

// Move moves the card to a new list by updating the "idList" field.
func (mc *MyCard) Move(newListID string) error {
	// Update the card's list by passing a map of parameters.
	args := map[string]string{
		"idList": newListID,
	}
	// Update is a method on adlio.Card.
	return mc.Update(args)
}

// AssignMember updates the card's assigned member(s) by setting "idMembers".
// Here we simply overwrite with the new member.
func (mc *MyCard) AssignMember(memberID string) error {
	args := map[string]string{
		"idMembers": memberID,
	}
	return mc.Update(args)
}

// PostComment posts a comment to the card using the Trello REST API.
func (mc *MyCard) PostComment(comment string, tc *TrelloClient) error {
	endpoint := fmt.Sprintf("https://api.trello.com/1/cards/%s/actions/comments", mc.ID)
	data := url.Values{}
	data.Set("text", comment)
	data.Set("key", tc.APIKey)
	data.Set("token", tc.Token)

	resp, err := http.PostForm(endpoint, data)
	if err != nil {
		return fmt.Errorf("failed to post comment: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := ioutil.ReadAll(resp.Body)
		return fmt.Errorf("failed to post comment, status: %d, response: %s", resp.StatusCode, string(body))
	}
	return nil
}

// AddComment posts a comment on the card using the Trello REST API.
// Note: We require a TrelloClient to supply APIKey and Token.
func (mc *MyCard) AddComment(comment string, tc *TrelloClient) error {
	endpoint := fmt.Sprintf("https://api.trello.com/1/cards/%s/actions/comments", mc.ID)
	data := url.Values{}
	data.Set("text", comment)
	data.Set("key", tc.APIKey)
	data.Set("token", tc.Token)

	resp, err := http.PostForm(endpoint, data)
	if err != nil {
		return fmt.Errorf("failed to post comment: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := ioutil.ReadAll(resp.Body)
		return fmt.Errorf("failed to post comment, status: %d, response: %s", resp.StatusCode, string(body))
	}
	return nil
}

// GetComments retrieves comments from the card using the TrelloClient.
func (mc *MyCard) GetComments(tc *TrelloClient) ([]string, error) {
	endpoint := fmt.Sprintf("https://api.trello.com/1/cards/%s/actions?filter=commentCard&key=%s&token=%s", mc.ID, tc.APIKey, tc.Token)
	resp, err := http.Get(endpoint)
	if err != nil {
		return nil, fmt.Errorf("failed to get comments: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := ioutil.ReadAll(resp.Body)
		return nil, fmt.Errorf("failed to get comments, status: %d, response: %s", resp.StatusCode, string(body))
	}
	var actions []struct {
		Data struct {
			Text string `json:"text"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&actions); err != nil {
		return nil, fmt.Errorf("failed to decode comments: %w", err)
	}
	var comments []string
	for _, action := range actions {
		comments = append(comments, action.Data.Text)
	}
	return comments, nil
}

// TrelloClient wraps the adlio/trello Client and holds credentials.
type TrelloClient struct {
	Client  *adlio.Client
	APIKey  string
	Token   string
	BoardID string
}

// NewTrelloClient
func NewTrelloClient(apiKey, token, boardID string) *TrelloClient {
	client := adlio.NewClient(apiKey, token)
	return &TrelloClient{
		Client:  client,
		APIKey:  apiKey,
		Token:   token,
		BoardID: boardID,
	}
}

// GetBoard retrieves the board by its BoardID.
func (tc *TrelloClient) GetBoard() (*adlio.Board, error) {
	return tc.Client.GetBoard(tc.BoardID, adlio.Defaults())
}

// GetBoardLists returns the lists on the board.
func (tc *TrelloClient) GetBoardLists() ([]*adlio.List, error) {
	board, err := tc.GetBoard()
	if err != nil {
		return nil, err
	}
	return board.GetLists(adlio.Defaults())
}

// CreateCard creates a new card with the given title and description in the specified list.
func (tc *TrelloClient) CreateCard(title, description, listID string) (*adlio.Card, error) {
	card := &adlio.Card{
		Name: title,
		Desc: description,
	}

	log.Println(listID)
	// Pass the card and an Arguments map with the list ID.
	err := tc.Client.CreateCard(card, adlio.Arguments{"idList": listID})
	if err != nil {
		return nil, fmt.Errorf("failed to create card: %w", err)
	}
	return card, nil
}

// GetListIDByName searches the board lists for one with the given name and returns its ID.
func (tc *TrelloClient) GetListIDByName(listName string) (string, error) {
	board, err := tc.GetBoard()
	if err != nil {
		return "", fmt.Errorf("failed to get board: %w", err)
	}
	lists, err := board.GetLists(adlio.Defaults())
	if err != nil {
		return "", fmt.Errorf("failed to get board lists: %w", err)
	}
	for _, list := range lists {
		if list.Name == listName {
			return list.ID, nil
		}
	}
	return "", fmt.Errorf("list '%s' not found", listName)
}

// GetDoneListID searches the board lists for one named "Done" and returns its ID.
func (tc *TrelloClient) GetDoneListID() (string, error) {
	lists, err := tc.GetBoardLists()
	if err != nil {
		return "", err
	}
	for _, list := range lists {
		if list.Name == "Done" {
			return list.ID, nil
		}
	}
	return "", fmt.Errorf("Done list not found")
}

// GetMember retrieves a Trello member by their ID using the underlying client.
func (tc *TrelloClient) GetMember(memberID string) (*adlio.Member, error) {
	return tc.Client.GetMember(memberID, adlio.Defaults())
}

// GetMemberByName searches the board for a member whose Username matches the provided name.
func (tc *TrelloClient) GetMemberByName(username string) (*adlio.Member, error) {
	// First, get the board.
	board, err := tc.GetBoard()
	if err != nil {
		return nil, fmt.Errorf("failed to get board: %w", err)
	}

	// Get all members of the board.
	// Assuming board.GetMembers returns []*adlio.Member. If not available, you may need to use another API call.
	members, err := board.GetMembers(adlio.Defaults())
	if err != nil {
		return nil, fmt.Errorf("failed to get board members: %w", err)
	}

	// Iterate through members and return the one that matches.
	for _, m := range members {
		if m.Username == username {
			return m, nil
		}
	}
	return nil, fmt.Errorf("member with username %s not found", username)
}

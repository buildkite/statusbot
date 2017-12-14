package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/davecgh/go-spew/spew"
	"github.com/nlopes/slack"
	statuspage "github.com/yfronto/go-statuspage-api"
)

const (
	defaultChannel = `G8DKQK0F5`
	statusPageID   = `ltljpr68dygn`
	baseURL        = `https://www.buildkitestatus.com`
)

func main() {
	updates, err := pollStatusPage(time.Second * 5)
	if err != nil {
		log.Fatal(err)
	}

	api := slack.New(os.Getenv(`SLACK_TOKEN`))
	rtm := api.NewRTM()
	go rtm.ManageConnection()

	cutoff := time.Now().AddDate(0, -1, 0)

	go func() {
		for update := range updates {
			if update.Timestamp.After(cutoff) {
				if err := update.PostToSlack(api); err != nil {
					log.Printf("Error posting to slack: %v", err)
					time.Sleep(time.Second * 5)
				}
			}
		}
	}()

	var botID string

	for {
		select {
		case msg := <-rtm.IncomingEvents:
			switch ev := msg.Data.(type) {
			case *slack.ConnectingEvent:
				// Attempting connection
			case *slack.ConnectedEvent:
				botID = ev.Info.User.ID
				log.Printf("Connected, bot id = %s", botID)
			case *slack.TeamJoinEvent:
				// Handle new user to client
			case *slack.MessageEvent:
				// Handle new message to channel
				log.Printf("Message from %s (%s): %s", ev.User, ev.BotID, ev.Msg.Text)
				spew.Dump(ev)
			case *slack.ReactionAddedEvent:
				// Handle reaction added
			case *slack.ReactionRemovedEvent:
				// Handle reaction removed
			case *slack.PresenceChangeEvent:
				// Handle presence change
			case *slack.RTMError:
				log.Printf("Error: %s", ev.Error())
			case *slack.AckErrorEvent:
				log.Printf("ACK Error: %s", ev)
			case *slack.InvalidAuthEvent:
				log.Fatal("Invalid credentials")
			default:
				log.Printf("Unknown message type: %T %#v", ev, msg)
			}
		}
	}
}

type incidentUpdate struct {
	Incident       statuspage.Incident
	IncidentUpdate statuspage.IncidentUpdate
	Timestamp      time.Time
	IsNew          bool
}

func (iu incidentUpdate) PostToSlack(api *slack.Client) error {
	var color string
	var title string
	switch status := *iu.IncidentUpdate.Status; status {
	// real-time incidents
	case `identified`:
		color = "#B03A2E"
		title = fmt.Sprintf("An incident has been identified: %s 🚨", *iu.Incident.Name)
	case `investigating`:
		title = fmt.Sprintf("We are investigating an incident: %s 🎉", *iu.Incident.Name)
		color = "#B03A2E"
	case `resolved`:
		title = fmt.Sprintf("An incident has been resolved: %s 🎉", *iu.Incident.Name)
		color = "#36a64f"
	case `monitoring`:
		title = fmt.Sprintf("We are monitoring an incident: %s 🎉", *iu.Incident.Name)
		color = "#36a64f"

	// scheduled incidents
	case `scheduled`:
		title = fmt.Sprintf("We've scheduled some downtime: %s", *iu.Incident.Name)
	case `inprogress`:
		title = fmt.Sprintf("Scheduled downtime is in progress: %s", *iu.Incident.Name)
	case `verifying`:
		title = fmt.Sprintf("Scheduled downtime is complete and we are monitoring: %s", *iu.Incident.Name)
	case `complete`:
		title = fmt.Sprintf("Scheduled downtime is complete: %s", *iu.Incident.Name)

	default:
		spew.Dump(iu.IncidentUpdate)
		log.Printf("Unhandled status %q", status)
		return nil
	}
	_, _, err := api.PostMessage(defaultChannel, "", slack.PostMessageParameters{
		Username: "Buildkite Status",
		AsUser:   false,
		IconURL:  "https://pbs.twimg.com/profile_images/543308685846392834/MFz0QmKq_400x400.jpeg",
		Attachments: []slack.Attachment{{
			Text:      *iu.IncidentUpdate.Body,
			Color:     color,
			Title:     title,
			TitleLink: fmt.Sprintf(`%s/incidents/%s`, baseURL, *iu.Incident.ID),
			Footer:    "buildkitestatus.com",
			Ts:        json.Number(strconv.FormatInt(iu.IncidentUpdate.CreatedAt.Unix(), 10)),
			Fields: []slack.AttachmentField{
				{
					Title: "Status",
					Value: strings.Title(*iu.IncidentUpdate.Status),
				},
			},
		}},
	})
	return err
}

func pollStatusPage(d time.Duration) (chan incidentUpdate, error) {
	client, err := statuspage.NewClient(os.Getenv(`STATUS_PAGE_TOKEN`), statusPageID)
	if err != nil {
		return nil, err
	}

	ch := make(chan incidentUpdate)
	ticker := time.NewTicker(d)

	var lastUpdated sync.Map
	var startTime time.Time = time.Now()

	go func() {
		for _ = range ticker.C {
			incidents, err := client.GetAllIncidents()
			if err != nil {
				log.Printf("Error fetching incidents: %v", err)
				time.Sleep(time.Second * 5)
				continue
			}

			for _, incident := range incidents {
				var updates []incidentUpdate

				if result, ok := lastUpdated.Load(*incident.ID); ok {
					updates = findIncidentUpdates(incident, result.(time.Time))
				} else {
					updates = findIncidentUpdates(incident, time.Time{})
				}

				for _, update := range updates {
					if update.Timestamp.After
					ch <- update
				}

				if len(updates) > 0 {
					log.Printf("Found %d updates for incident `%s`", len(updates), *incident.ID)
					lastUpdated.Store(*incident.ID, updates[0].Timestamp)
				}
			}
		}
	}()

	return ch, nil
}

func findIncidentUpdates(incident statuspage.Incident, after time.Time) []incidentUpdate {
	var updates []incidentUpdate

	for idx, update := range incident.IncidentUpdates {
		if update.CreatedAt.After(after) {
			updates = append(updates, incidentUpdate{
				Incident:       incident,
				IncidentUpdate: *update,
				Timestamp:      *update.CreatedAt,
				IsNew:          (idx == 0),
			})
		}
	}

	// reverse order of updates
	for i, j := 0, len(updates)-1; i < j; i, j = i+1, j-1 {
		updates[i], updates[j] = updates[j], updates[i]
	}

	return updates
}

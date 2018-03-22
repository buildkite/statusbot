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
	baseURL = `https://buildkitestatus.com`
	version = "dev"
)

func main() {
	log.Printf("Statusbot (%s) starting", version)

	updates, err := pollStatusPage(time.Second*30, time.Now())
	if err != nil {
		log.Fatal(err)
	}

	api := slack.New(os.Getenv(`SLACK_TOKEN`))

	// logger := log.New(os.Stdout, "slack-bot: ", log.Lshortfile|log.LstdFlags)
	// slack.SetLogger(logger)
	// api.SetDebug(true)

	rtm := api.NewRTM()
	go rtm.ManageConnection()

	go func() {
		for update := range updates {
			if err := update.PostToAllSlackChannels(api); err != nil {
				log.Printf("Error posting to slack: %v", err)
				time.Sleep(time.Second * 5)
			}
		}
	}()

	channels, err := getBotChannels(api)
	if err != nil {
		log.Fatal(err)
	}

	for _, channel := range channels {
		log.Printf("In channel %s (%s)", channel.Name, channel.ID)
	}

	var botID string

	for {
		select {
		case msg := <-rtm.IncomingEvents:
			switch ev := msg.Data.(type) {

			case *slack.HelloEvent:
				// Not sure
			case *slack.ConnectingEvent:
				// Attempting connection
			case *slack.ConnectedEvent:
				botID = ev.Info.User.ID
				log.Printf("Connected, bot id = %s", botID)

			case *slack.TeamJoinEvent:
				// Handle new user to client
			case *slack.MessageEvent:
				// Handle new message to channel
				// if ev.Username != `Buildkite Status` {
				// 	spew.Dump(ev)
				// }

			case *slack.ReactionAddedEvent:
				// Handle reaction added
			case *slack.ReactionRemovedEvent:
				// Handle reaction removed
			case *slack.PresenceChangeEvent:
				// Handle presence change
			case *slack.ChannelJoinedEvent:
				log.Printf("Joined channel: %#v", ev)
				// Handle channel joined
			case *slack.ChannelLeftEvent:
				// Handle channel left

			case *slack.RTMError:
				log.Printf("Error: %v", ev)
			case *slack.ConnectionErrorEvent:
				log.Printf("Error: %s", ev.Error())
			case *slack.InvalidAuthEvent:
				log.Fatal("Invalid credentials")
			case *slack.AckErrorEvent:
				log.Printf("ACK Error")

				// default:
				// log.Printf("Unknown message type: %T %#v", ev, msg)
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

func (iu incidentUpdate) PostToAllSlackChannels(api *slack.Client) error {
	attachment := slack.Attachment{
		Text:      *iu.IncidentUpdate.Body,
		TitleLink: fmt.Sprintf(`%s/incidents/%s`, baseURL, *iu.Incident.ID),
		Footer:    "buildkitestatus.com",
		Ts:        json.Number(strconv.FormatInt(iu.IncidentUpdate.CreatedAt.Unix(), 10)),
		Fields: []slack.AttachmentField{
			{
				Title: "Status",
				Value: strings.Title(*iu.IncidentUpdate.Status),
			},
		},
	}

	switch status := *iu.IncidentUpdate.Status; status {
	// real-time incidents
	case `identified`:
		attachment.Color = "#B03A2E"
		attachment.Title = fmt.Sprintf("An incident has been identified: %s 🚨", *iu.Incident.Name)
	case `investigating`:
		attachment.Title = fmt.Sprintf("We are investigating an incident: %s 🎉", *iu.Incident.Name)
		attachment.Color = "#B03A2E"
	case `resolved`:
		attachment.Title = fmt.Sprintf("An incident has been resolved: %s 🎉", *iu.Incident.Name)
		attachment.Color = "#36a64f"
	case `monitoring`:
		attachment.Title = fmt.Sprintf("We are monitoring an incident: %s 🕵️‍", *iu.Incident.Name)
		attachment.Color = "#36a64f"
	case `postmortem`:
		attachment.Title = fmt.Sprintf("We've posted a postmortem for an incident: %s 🎉", *iu.Incident.Name)
		attachment.Text = ""

	// scheduled incidents
	case `scheduled`:
		attachment.Title = fmt.Sprintf("We've scheduled some downtime: %s", *iu.Incident.Name)
	case `inprogress`:
		attachment.Title = fmt.Sprintf("Scheduled downtime is in progress: %s", *iu.Incident.Name)
	case `verifying`:
		attachment.Title = fmt.Sprintf("Scheduled downtime is complete and we are monitoring: %s", *iu.Incident.Name)
	case `completed`:
		attachment.Title = fmt.Sprintf("Scheduled downtime is complete: %s", *iu.Incident.Name)

	default:
		spew.Dump(iu.IncidentUpdate)
		log.Printf("Unhandled status %q", status)
		return nil
	}

	channels, err := getBotChannels(api)
	if err != nil {
		return err
	}

	if len(channels) == 0 {
		log.Printf("Not in any channels!")
	}

	for _, channel := range channels {
		log.Printf("Posting to %#v", channel)
		_, _, err := api.PostMessage(channel.ID, "", slack.PostMessageParameters{
			Username:    "Buildkite Status",
			AsUser:      false,
			IconURL:     "https://pbs.twimg.com/profile_images/543308685846392834/MFz0QmKq_400x400.jpeg",
			Attachments: []slack.Attachment{attachment},
		})
		if err != nil {
			return err
		}
	}

	return nil
}

func getBotChannels(api *slack.Client) ([]slack.Channel, error) {
	results := []slack.Channel{}

	channels, err := api.GetChannels(true)
	if err != nil {
		return nil, err
	}

	for _, channel := range channels {
		if channel.IsMember {
			results = append(results, channel)
		}
	}

	return results, nil
}

func pollStatusPage(d time.Duration, cutoff time.Time) (chan incidentUpdate, error) {
	pageID := os.Getenv(`STATUS_PAGE_ID`)
	log.Printf("Polling statuspage.io (Page %s)", pageID)

	client, err := statuspage.NewClient(os.Getenv(`STATUS_PAGE_TOKEN`), pageID)
	if err != nil {
		return nil, err
	}

	ch := make(chan incidentUpdate)
	ticker := time.NewTicker(d)

	var lastUpdated sync.Map
	var startTime time.Time = cutoff

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
					updates = convertToIncidentUpdates(incident, result.(time.Time))
				} else {
					updates = convertToIncidentUpdates(incident, time.Time{})
				}

				for _, update := range updates {
					if update.Timestamp.After(startTime) {
						log.Printf("Found an update")
						ch <- update
					}
				}

				if len(updates) > 0 {
					lastUpdated.Store(*incident.ID, updates[0].Timestamp)
				}
			}

			startTime = time.Now()
		}
	}()

	return ch, nil
}

func convertToIncidentUpdates(incident statuspage.Incident, after time.Time) []incidentUpdate {
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

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mmcdole/gofeed/atom"
	"github.com/slack-go/slack"
)

const (
	baseURL    = `https://buildkitestatus.com`
	version    = "dev"
	dateFormat = `2006-01-02`
)

func main() {
	fetchFeedURL := flag.String("atom-feed", "", "An atom feed to parse instead of webhooks")
	afterString := flag.String("after", "", "Only post updates after this date (YYYY-MM-DD)")
	dryRun := flag.Bool("dry-run", false, "If true, don't actually post slack messages")
	flag.Parse()

	var after time.Time
	if *afterString != "" {
		var err error
		after, err = time.Parse(dateFormat, *afterString)
		if err != nil {
			log.Fatalf("Failed to parse --after (needs YYYY-MM-DD): %v", err)
		}
	}

	log.Printf("Statusbot (%s) starting", version)

	api := slack.New(os.Getenv(`SLACK_TOKEN`))
	rtm := api.NewRTM()
	go rtm.ManageConnection()

	channels, err := getBotChannels(api)
	if err != nil {
		log.Fatal(err)
	}

	// For debugging, lets show what channels we are in
	for _, channel := range channels {
		log.Printf("In channel %s", channel.Name)
	}

	// Run in either atom feed mode or webhook server mode
	if *fetchFeedURL != "" {
		if err := processAtomFeed(*fetchFeedURL, api, after, *dryRun); err != nil {
			log.Fatal(err)
		}
		os.Exit(0)
	} else {
		startWebhookServer(api, *dryRun)
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
				log.Printf("Left channel: %#v", ev)

			case *slack.RTMError:
				log.Printf("Error: %v", ev)
			case *slack.ConnectionErrorEvent:
				log.Printf("Error: %s", ev.Error())
			case *slack.InvalidAuthEvent:
				log.Fatal("Invalid credentials")
			case *slack.AckErrorEvent:
				log.Printf("ACK Error")

				// default:
				// 	log.Printf("Unknown message type: %T %#v", ev, msg)
			}
		}
	}
}

func startWebhookServer(api *slack.Client, dryRun bool) {
	var mu sync.Mutex

	// The webhook server receives webhooks from statuspage.io
	// and sends them on to slack
	server := &StatusPageWebhookServer{
		Handler: func(w StatusPageWebhookNotification) {
			// Lock this to prevent concurrent notification runs as
			// these are now asynchronously dispatched
			mu.Lock()
			defer mu.Unlock()
			log.Printf("Handling webhook notification")

			for _, update := range w.Incident.IncidentUpdates {
				if err := postIncidentUpdateToAllSlackChannels(w.Incident.Name, update, api, dryRun); err != nil {
					log.Printf("Error posting updates: %v", err)
					return
				}
			}
		},
	}

	listen := ":8080"
	if port := os.Getenv("PORT"); port != "" {
		listen = ":" + port
	}

	log.Printf("Webhook server started, listening on %s", listen)
	go func() {
		http.Handle("/", server)
		log.Fatal(http.ListenAndServe(listen, nil))
	}()
}

func incidentURL(update StatusPageIncidentUpdate) string {
	return fmt.Sprintf(`%s/incidents/%s`, baseURL, update.IncidentID)
}

func postIncidentUpdateToAllSlackChannels(name string, update StatusPageIncidentUpdate, api *slack.Client, dryRun bool) error {
	attachment := slack.Attachment{
		Text:       update.Body,
		TitleLink:  incidentURL(update),
		Footer:     "buildkitestatus.com",
		Ts:         json.Number(strconv.FormatInt(update.CreatedAt.Unix(), 10)),
		CallbackID: update.ID,
		Fields: []slack.AttachmentField{
			{
				Title: "Status",
				Value: strings.Title(update.Status),
			},
		},
	}

	switch status := update.Status; status {
	// real-time incidents
	case `identified`:
		attachment.Color = "#B03A2E"
		attachment.Title = fmt.Sprintf("An incident has been identified: %s 💡", name)
	case `investigating`:
		attachment.Title = fmt.Sprintf("We are investigating an incident: %s 🚨", name)
		attachment.Color = "#B03A2E"
	case `resolved`:
		attachment.Title = fmt.Sprintf("An incident has been resolved: %s 🎉", name)
		attachment.Color = "#36a64f"
	case `monitoring`:
		attachment.Title = fmt.Sprintf("We are monitoring an incident: %s 👀️‍", name)
		attachment.Color = "#36a64f"
	case `postmortem`:
		attachment.Title = fmt.Sprintf("We've posted a postmortem for an incident: %s ⚖️", name)
		attachment.Text = ""

	// scheduled incidents
	case `scheduled`:
		attachment.Title = fmt.Sprintf("We've scheduled some downtime: %s", name)
	case `inprogress`, `in_progress`:
		attachment.Title = fmt.Sprintf("Scheduled downtime is in progress: %s", name)
	case `verifying`:
		attachment.Title = fmt.Sprintf("Scheduled downtime is complete and we are monitoring: %s", name)
	case `completed`:
		attachment.Title = fmt.Sprintf("Scheduled downtime is complete: %s", name)

	default:
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
		inHistory, err := isUpdateInChannelHistory(update, api, channel)
		if inHistory && err == nil {
			log.Printf("Skipping already posted update %s to %s", update.ID, channel.Name)
			continue
		} else if err != nil {
			log.Printf("Failed to fetch history, skipping posting update")
			continue
		}
		if dryRun {
			log.Printf("Posting update %s to %#v (DRY RUN)", update.ID, channel.Name)
		} else {
			log.Printf("Posting update %s to %#v", update.ID, channel.Name)
		}
		if !dryRun {
			_, _, err := api.PostMessage(channel.Name,
				slack.MsgOptionUsername("Buildkite Status"),
				slack.MsgOptionAsUser(false),
				slack.MsgOptionIconURL("https://pbs.twimg.com/profile_images/1369491664520155144/aoJIAPTf_400x400.jpg"),
				slack.MsgOptionAttachments(attachment),
			)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

type Channel struct {
	ID   string
	Name string
}

// getBotChannels returns the channels that the bot is a member of
func getBotChannels(api *slack.Client) ([]Channel, error) {
	results := []Channel{}

	// Conversations cover channels and shared channels
	conversation, nextCursor, err := api.GetConversations(&slack.GetConversationsParameters{
		ExcludeArchived: "true",
		Types:           []string{"public_channel", "private_channel"},
		Limit:           1000,
	})
	if err != nil {
		return nil, err
	}

	// TODO: add looping when we get to >1000 channels
	if nextCursor != "" {
		log.Printf("⚠️ conversation.list returned a next cursor token, there may be more channels!")
	}

	// Check if we are a member of the conversations
	for _, conversation := range conversation {
		if conversation.IsMember {
			results = append(results, Channel{
				ID:   conversation.ID,
				Name: fmt.Sprintf("#%s", conversation.NameNormalized),
			})
		}
	}

	return results, nil
}

func isUpdateInChannelHistory(update StatusPageIncidentUpdate, api *slack.Client, channel Channel) (bool, error) {
	historyResp, err := api.GetConversationHistory(&slack.GetConversationHistoryParameters{
		ChannelID: channel.ID,
	})
	if err != nil {
		log.Printf("Error getting conversation history: %v", err)
		return false, err
	}

	// Get all the channel hisitory
	for _, message := range historyResp.Messages {
		if message.User != "" {
			continue
		}
		if len(message.Attachments) > 0 {
			for _, attachment := range message.Attachments {
				// For modern attachments, we can use the update ID in the CallbackID
				if attachment.CallbackID == update.ID {
					return true, nil
				}
				// For older attachments, we use the title link and timestamp
				if attachment.TitleLink == incidentURL(update) &&
					attachment.Ts == json.Number(strconv.FormatInt(update.CreatedAt.Unix(), 10)) {
					return true, nil
				}
			}
		}
	}

	return false, nil
}

type StatusPageWebhookNotification struct {
	Meta struct {
		Unsubscribe   string    `json:"unsubscribe"`
		Documentation string    `json:"documentation"`
		GeneratedAt   time.Time `json:"generated_at"`
	} `json:"meta"`
	Page struct {
		ID                string `json:"id"`
		StatusIndicator   string `json:"status_indicator"`
		StatusDescription string `json:"status_description"`
	} `json:"page"`
	Incident StatusPageIncident `json:"incident"`
}

type StatusPageIncident struct {
	Name                          string                     `json:"name"`
	Status                        string                     `json:"status"`
	CreatedAt                     time.Time                  `json:"created_at"`
	UpdatedAt                     time.Time                  `json:"updated_at"`
	MonitoringAt                  time.Time                  `json:"monitoring_at"`
	ResolvedAt                    time.Time                  `json:"resolved_at"`
	Impact                        string                     `json:"impact"`
	Shortlink                     string                     `json:"shortlink"`
	ScheduledFor                  interface{}                `json:"scheduled_for"`
	ScheduledUntil                interface{}                `json:"scheduled_until"`
	ScheduledRemindPrior          bool                       `json:"scheduled_remind_prior"`
	ScheduledRemindedAt           time.Time                  `json:"scheduled_reminded_at"`
	ImpactOverride                interface{}                `json:"impact_override"`
	ScheduledAutoInProgress       bool                       `json:"scheduled_auto_in_progress"`
	ScheduledAutoCompleted        bool                       `json:"scheduled_auto_completed"`
	ID                            string                     `json:"id"`
	PageID                        string                     `json:"page_id"`
	IncidentUpdates               []StatusPageIncidentUpdate `json:"incident_updates"`
	PostmortemBody                string                     `json:"postmortem_body"`
	PostmortemBodyLastUpdatedAt   time.Time                  `json:"postmortem_body_last_updated_at"`
	PostmortemIgnored             bool                       `json:"postmortem_ignored"`
	PostmortemPublishedAt         time.Time                  `json:"postmortem_published_at"`
	PostmortemNotifiedSubscribers bool                       `json:"postmortem_notified_subscribers"`
	PostmortemNotifiedTwitter     bool                       `json:"postmortem_notified_twitter"`
}

type StatusPageIncidentUpdate struct {
	Status             string    `json:"status"`
	Body               string    `json:"body"`
	CreatedAt          time.Time `json:"created_at"`
	WantsTwitterUpdate bool      `json:"wants_twitter_update"`
	TwitterUpdatedAt   time.Time `json:"twitter_updated_at"`
	UpdatedAt          time.Time `json:"updated_at"`
	DisplayAt          time.Time `json:"display_at"`
	AffectedComponents []struct {
		Code      string `json:"code"`
		Name      string `json:"name"`
		OldStatus string `json:"old_status"`
		NewStatus string `json:"new_status"`
	} `json:"affected_components"`
	CustomTweet          string `json:"custom_tweet"`
	DeliverNotifications bool   `json:"deliver_notifications"`
	TweetID              int64  `json:"tweet_id"`
	ID                   string `json:"id"`
	IncidentID           string `json:"incident_id"`
}

type StatusPageWebhookServer struct {
	Handler func(w StatusPageWebhookNotification)
}

func (s *StatusPageWebhookServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	log.Printf("%s %s", r.Method, r.URL.Path)

	// read the webhook body
	payload, err := ioutil.ReadAll(r.Body)
	if err != nil {
		log.Printf("Error reading body: %v", err)
		return
	}
	defer r.Body.Close()

	// parse the json into a map to detect component vs incident updates
	var parsed map[string]interface{}
	if err := json.Unmarshal(payload, &parsed); err != nil {
		log.Printf("Error unmarshalling webhook: %v", err)
		return
	}

	if _, ok := parsed["component"]; ok {
		log.Printf("Skipping component update webhook")
		return
	}

	// var formatted bytes.Buffer
	// _ = json.Indent(&formatted, payload, "", " ")
	// log.Printf("Raw: %s", formatted.String())

	var webhook StatusPageWebhookNotification

	// parse the json into a webhook
	if err := json.Unmarshal(payload, &webhook); err != nil {
		log.Printf("Error unmarshalling webhook: %v", err)
		return
	}

	// reverse order of updates to be in chronological order
	updates := webhook.Incident.IncidentUpdates
	for i, j := 0, len(updates)-1; i < j; i, j = i+1, j-1 {
		updates[i], updates[j] = updates[j], updates[i]
	}
	webhook.Incident.IncidentUpdates = updates

	// handle the webhook asynchronously so we get a reply back quick
	go s.Handler(webhook)
}

func processAtomFeed(feedURL string, api *slack.Client, after time.Time, dryRun bool) error {
	feedContent, err := fetchURL(feedURL)
	if err != nil {
		return err
	}

	fp := atom.Parser{}
	atomFeed, err := fp.Parse(bytes.NewReader(feedContent))
	if err != nil {
		return err
	}

	for _, entry := range atomFeed.Entries {
		if entry.PublishedParsed.Before(after) {
			log.Printf("Finishing processing feed, %s is before cutoff %s",
				entry.PublishedParsed.Format(dateFormat), after.Format(dateFormat))
			return nil
		}

		if len(entry.Links) == 0 {
			return errors.New("No links found in entry")
		}

		payload, err := fetchURL(entry.Links[0].Href + ".json")
		if err != nil {
			return err
		}

		// var formatted bytes.Buffer
		// _ = json.Indent(&formatted, payload, "", " ")
		// log.Printf("Raw: %s", formatted.String())

		var incident StatusPageIncident

		if err := json.Unmarshal(payload, &incident); err != nil {
			return err
		}

		log.Printf("Processing incident %s", incident.ID)

		for _, update := range incident.IncidentUpdates {
			if err := postIncidentUpdateToAllSlackChannels(incident.Name, update, api, dryRun); err != nil {
				return err
			}
		}
	}

	return nil
}

func fetchURL(u string) ([]byte, error) {
	log.Printf("Fetching %s", u)

	resp, err := http.Get(u)
	if err != nil {
		return nil, err
	}

	defer resp.Body.Close()
	return ioutil.ReadAll(resp.Body)
}

package main

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/davecgh/go-spew/spew"
	"github.com/nlopes/slack"
)

const (
	baseURL    = `https://buildkitestatus.com`
	version    = "dev"
	dateFormat = `2006-01-02`
)

func main() {
	fetchFeedURL := flag.String("atom-feed", "", "An atom feed to parse instead of webhooks")
	pollFeedURL := flag.String("poll-atom-feed", "", "An atom feed to poll alongside webhooks")
	pollFrequency := flag.Duration("poll-frequency", 5*time.Minute, "How often to poll the atom feed")
	afterString := flag.String("after", "", "Only post updates after this date (YYYY-MM-DD)")
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

	// Run in either atom feed mode or webhook server mode
	if *fetchFeedURL != "" {
		if err := processAtomFeed(*fetchFeedURL, api, after); err != nil {
			log.Fatal(err)
		}
		os.Exit(0)
	} else {
		if *pollFeedURL != "" {
			log.Printf("Polling atom feed every %v", *pollFrequency)
			pollAtomFeed(*pollFrequency, *pollFeedURL, api, after)
		}

		startWebhookServer(api)
	}

	channels, err := getBotChannels(api)
	if err != nil {
		log.Fatal(err)
	}

	// For debugging, lets show what channels we are in
	for _, channel := range channels {
		log.Printf("In channel %s", channel.Name)
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

func startWebhookServer(api *slack.Client) {
	// The webhook server receives webhooks from statuspage.io
	// and sends them on to slack
	server := &StatusPageWebhookServer{
		Handler: func(w StatusPageWebhookNotification) error {
			for _, update := range w.Incident.IncidentUpdates {
				if err := postIncidentUpdateToAllSlackChannels(w.Incident.Name, update, api); err != nil {
					return err
				}
			}
			return nil
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

func postIncidentUpdateToAllSlackChannels(name string, update StatusPageIncidentUpdate, api *slack.Client) error {
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
	case `inprogress`:
		attachment.Title = fmt.Sprintf("Scheduled downtime is in progress: %s", name)
	case `verifying`:
		attachment.Title = fmt.Sprintf("Scheduled downtime is complete and we are monitoring: %s", name)
	case `completed`:
		attachment.Title = fmt.Sprintf("Scheduled downtime is complete: %s", name)

	default:
		spew.Dump(update)
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
		if isUpdateInChannelHistory(update, api, channel) {
			log.Printf("Skipping already posted update %s to %s", update.ID, channel.Name)
			continue
		}
		log.Printf("Posting update %s to %#v", update.ID, channel.Name)
		_, _, err := api.PostMessage(channel.Name, "", slack.PostMessageParameters{
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

type Channel struct {
	ID   string
	Name string
}

// getBotChannels returns the channels that the bot is a member of
func getBotChannels(api *slack.Client) ([]Channel, error) {
	results := []Channel{}

	// Conversations cover channels and shared channels
	conversation, _, err := api.GetConversations(&slack.GetConversationsParameters{
		ExcludeArchived: "true",
		Types:           []string{"public_channel", "private_channel"},
	})
	if err != nil {
		return nil, err
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

func isUpdateInChannelHistory(update StatusPageIncidentUpdate, api *slack.Client, channel Channel) bool {
	historyResp, err := api.GetConversationHistory(&slack.GetConversationHistoryParameters{
		ChannelID: channel.ID,
	})
	if err != nil {
		log.Printf("Error getting conversation history: %v", err)
		return false
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
					return true
				}
				// For older attachments, we use the title link and timestamp
				if attachment.TitleLink == incidentURL(update) &&
					attachment.Ts == json.Number(strconv.FormatInt(update.CreatedAt.Unix(), 10)) {
					return true
				}
			}
		}
	}

	return false
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
	Handler func(w StatusPageWebhookNotification) error
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

	var formatted bytes.Buffer
	_ = json.Indent(&formatted, payload, "", " ")
	log.Printf("Raw: %s", formatted.String())

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

	// handle the webhook
	if err := s.Handler(webhook); err != nil {
		log.Printf("Handler failed: %v", err)
		return
	}
}

func pollAtomFeed(d time.Duration, feedURL string, api *slack.Client, after time.Time) {
	process := func() {
		if err := processAtomFeed(feedURL, api, after); err != nil {
			log.Printf("Error processing feed: %v", err)
		}
	}

	process()

	ticker := time.NewTicker(d)
	go func() {
		for {
			select {
			case <-ticker.C:
				process()
			}
		}
	}()
}

func processAtomFeed(feedURL string, api *slack.Client, after time.Time) error {
	xmlContent, err := fetchURL(feedURL)
	if err != nil {
		return nil
	}

	// if we get no content, nothing has changed
	if xmlContent == nil {
		return nil
	}

	var atomFeed struct {
		Entries []struct {
			Published string `xml:"published"`
			Link      struct {
				Href string `xml:"href,attr"`
			} `xml:"link"`
		} `xml:"entry"`
	}

	if err = xml.Unmarshal(xmlContent, &atomFeed); err != nil {
		return err
	}

	for _, entry := range atomFeed.Entries {
		published, err := time.Parse(`2006-01-02T15:04:05-07:00`, entry.Published)
		if err != nil {
			return fmt.Errorf("Error parsing published date: %v", err)
		}

		if published.Before(after) {
			log.Printf("Finishing processing feed, %s is before cutoff %s",
				published.Format(dateFormat), after.Format(dateFormat))
			return nil
		}

		payload, err := fetchURL(entry.Link.Href + ".json")
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
			if err := postIncidentUpdateToAllSlackChannels(incident.Name, update, api); err != nil {
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

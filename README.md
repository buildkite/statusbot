# Statusbot

**⚠️ This repo is archived. We recommend using the [first-party slack integration](https://support.atlassian.com/statuspage/docs/enable-slack-subscriptions/) instead ⚠️**

Statusbot is a Slackbot for Buildkite's Private and Public slack that helps interface customers with our monitoring services and site status information.

## Incident Updates

We use https://www.buildkitestatus.com/ to report on infrastructure incidents. Statusbot listens for notification webhooks from statuspage.io and posts slack messages when an incident is created and then posts updates as they occur.

![Example of the incident output](https://lachlan.me/s/20171214-1x8q76fdpatikdi.png)

## Deployment

Statusbot can be hosted on Heroku (for your own Slack's):

```bash
heroku create
heroku config:set SLACK_TOKEN=${SLACK_TOKEN}
git push heroku master
heroku ps:scale web=1
heroku logs -d web --tail
```

Get the url from the Heroku and add it as a notification to our Status Page at https://buildkitestatus.com:

* Select "Subscribe to Updates"
* Select the webhook tab
* Enter https://<your-app-name>.herokuapp.com/ as the URL to send webhooks to
* Enter an email for backup
* Press subscribe!

## Help! We missed a webhook!

Sometimes StatusPage.io disables webhooks on Heroku when they don't respond quickly enough. For cases like this, you can run statusbot against the Atom feed as well:

```
go run *.go --atom-feed https://www.buildkitestatus.com/history.atom --after "2019-04-01"
```

This will run a once-off update, skipping any already posted updates.

## Development

```bash
go run *.go
```

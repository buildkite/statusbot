# Statusbot

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
heroku ps:scale worker=1
heroku logs -d worker --tail
```

## Development

```bash
go run *.go
```

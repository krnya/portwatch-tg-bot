# PortWatch Bot

A Go bot that reads RSS feeds and sends new articles to a Telegram chat. By default, it checks `https://portwatch-vi.xyz/news/feed.xml` every two minutes.

## Running the bot

```sh
cp .env.example .env
```

Set `TELEGRAM_BOT_TOKEN` from BotFather and `TELEGRAM_CHAT_ID` in `.env`. For a private chat, open a conversation with the bot and press Start. For a group or channel, give the bot permission to send messages. `TELEGRAM_CHAT_ID` accepts a numeric chat ID or the `@username` of a public channel.

```sh
docker compose up -d --build
docker compose logs -f portwatch
```

The first successful poll of each source records its existing articles. Notifications start with articles discovered on later polls. The `portwatch-data` Docker volume preserves the history and delivery queue across restarts. Run one bot instance per volume.

## Updating sources

Edit [`config/sources.yaml`](config/sources.yaml). The bot reloads it before the next poll. Save changes by atomically replacing the file where possible. If the bot cannot load the new configuration, it keeps using the previous list. Set `sources: []` to pause polling.

```yaml
poll_interval: 2m
sources:
  - id: portwatch-vi
    kind: portwatch
    feed_url: https://portwatch-vi.xyz/news/feed.xml
```

If PortWatch moves to a new domain, update `feed_url` and keep the same `id` and `kind`. To add an independent RSS feed, use a new `id`, set `kind: rss`, and provide its HTTPS URL. Removing a source preserves its history and any queued messages. To change the destination chat, update `.env` and run `docker compose up -d`. Pending messages will go to the new chat.

If Telegram accepts a message just before the connection drops, a retry may produce a duplicate. After downtime, the bot can pick up articles still present in the feed. The PortWatch feed holds up to 30 articles.

## Publishing the Docker image

The [GitHub Actions workflow](.github/workflows/build.yaml) runs on pushes to `main`. It builds the Docker image for `linux/amd64` and `linux/arm64`, publishes it to `ghcr.io/<owner>/<repo>` with the `latest` and full commit SHA tags, and stops after publishing.

Publishing uses the built-in `GITHUB_TOKEN` with `packages: write` permission. If a package with that name already exists, grant the repository access in the package settings. [GHCR authentication](https://docs.github.com/en/packages/working-with-a-github-packages-registry/working-with-the-container-registry#authenticating-in-a-github-actions-workflow)

## Development

```sh
go test ./...
go vet ./...
```

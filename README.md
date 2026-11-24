# PortWatch Bot

A Go bot that sends new RSS articles to Telegram subscribers. By default, it checks `https://portwatch-vi.xyz/news/feed.xml` every two minutes.

## Running the bot

```sh
cp .env.example .env
```

Set `TELEGRAM_BOT_TOKEN` from BotFather in `.env`. Set `TELEGRAM_OWNER_ID` to your numeric Telegram user ID to manage domains from your private chat with the bot.

`TELEGRAM_CHAT_ID` is optional and configures an additional destination for all news. It accepts a numeric ID or the `@username` of a public channel. Give the bot permission to post if you use a group or channel.

```sh
docker compose up -d --build
docker compose logs -f portwatch
```

Anyone can open a private chat with the bot and press **Start** or send `/subscribe` to receive new articles. Send `/unsubscribe` to stop receiving them. The first successful poll of each source records existing articles, and joining the mailing list starts delivery with the next new article. The `portwatch-data` Docker volume preserves subscriptions, article history, and queued deliveries across restarts. Run one bot instance per bot token and volume.

## Updating sources

Only the Telegram user configured in `TELEGRAM_OWNER_ID` can manage domains through the bot. Send these commands in a private chat:

- `/domains` lists the active sources and their IDs.
- `/setdomain portwatch-vi new.example.org` moves a source to another domain while keeping its feed path and article history.
- `/adddomain another new.example.org` adds a PortWatch-style source with the `/news/feed.xml` path.
- `/removedomain another` removes a source from polling.

The add and change commands also accept a full HTTPS feed URL. Changes are stored in SQLite and take precedence over entries with the same ID in [`config/sources.yaml`](config/sources.yaml).

The YAML file supplies the initial sources and polling interval. The bot reloads it before the next poll and keeps the previous configuration if the edit is invalid.

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

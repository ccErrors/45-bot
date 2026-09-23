# race-bot

Telegram-бот, который записывает велозаезды по трансляции геопозиции и рисует трек на карте OSM
с раскраской по скорости. ТЗ: [docs/SPEC.md](docs/SPEC.md).

## Запуск

```sh
export TELEGRAM_TOKEN=123456:ABC...        # токен от @BotFather
export TILE_USER_AGENT="race-bot/1.0 (you@example.com)"
go run ./cmd/bot
```

Остальные настройки (`DB_PATH`, `TZ`, `IDLE_TIMEOUT`, …) — в разделе 6 ТЗ.

## Использование

1. В чате с ботом: 📎 → Геопозиция → «Транслировать геопозицию».
2. Остановите трансляцию — бот пришлёт итог заезда.
3. `/races` — список заездов, `/race_<id>` — карта трека.

## Деплой

GitHub Actions по SSH на сервер с systemd, секреты и переменные — [docs/DEPLOY.md](docs/DEPLOY.md).

## Отладка рендера

```sh
go run ./cmd/render -csv testdata/ride.csv -o race.png        # тестовый заезд → картинка
go run ./cmd/render -race 1a -o race.png                      # заезд из БД
go run ./cmd/render -csv testdata/ride.csv -seed-user <tg_id> # положить тестовый заезд в БД,
                                                              # потом /races в боте
go test ./...
```

`testdata/ride.csv` — круг ~10 км по Москве со всеми неудобными случаями: остановка,
сбой GPS (скачок на 700 м), повтор времени. Формат CSV: `lat,lon,unix_ts`, строки с `#` — комментарии.

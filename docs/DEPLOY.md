# Деплой

При пуше в `main` (или ручном запуске во вкладке **Actions → deploy → Run workflow**) GitHub Actions:

1. прогоняет `go vet` и `go test`;
2. собирает статический бинарник под Linux (`CGO_ENABLED=0`);
3. по SSH заливает бинарник, файл настроек и systemd-юнит на сервер;
4. перезапускает сервис и проверяет, что он поднялся (иначе показывает последние 50 строк лога и падает).

Бот работает как **systemd user-сервис** отдельного пользователя, root для деплоя не нужен.

```
~/race-bot/race-bot        бинарник (предыдущий — race-bot.prev)
~/race-bot/env             настройки бота, права 600, перезаписывается при каждом деплое
~/race-bot/data/bot.db     база — деплой её не трогает
~/race-bot/data/tiles/     кэш тайлов OSM
```

## Первичная настройка (один раз)

**1. Ключ для деплоя** — на своей машине:

```sh
ssh-keygen -t ed25519 -f race-bot-deploy -N "" -C race-bot-deploy
# race-bot-deploy      → приватный ключ, в секрет DEPLOY_SSH_KEY
# race-bot-deploy.pub  → публичный, на сервер
```

**2. Сервер** — любой Linux с systemd (Ubuntu/Debian и т.п.):

```sh
scp deploy/setup-server.sh root@<сервер>:
ssh root@<сервер> bash setup-server.sh racebot "$(cat race-bot-deploy.pub)"
```

Скрипт создаёт пользователя `racebot`, включает `loginctl enable-linger` (чтобы сервис жил без открытой сессии)
и добавляет ключ в `authorized_keys`.

**3. Отпечаток сервера** — для защиты от подмены хоста:

```sh
ssh-keyscan -p 22 <сервер> > known_hosts   # значение секрета DEPLOY_KNOWN_HOSTS
```

**4. Секреты и переменные в GitHub** — Settings → Environments → создать `production`,
туда добавить секреты и переменные (или на уровне репозитория: Settings → Secrets and variables → Actions).

## Секреты (Secrets)

| Имя                  | Обязателен | Что это |
|----------------------|:----------:|---------|
| `TELEGRAM_TOKEN`     | да | Токен бота от @BotFather |
| `DEPLOY_HOST`        | да | IP или домен сервера |
| `DEPLOY_USER`        | да | Пользователь для деплоя (`racebot`) |
| `DEPLOY_SSH_KEY`     | да | Приватный ключ целиком, включая `-----BEGIN/END ...-----` |
| `DEPLOY_KNOWN_HOSTS` | да | Вывод `ssh-keyscan` для сервера |

## Переменные (Variables) — все необязательные

| Имя               | По умолчанию | Что это |
|-------------------|--------------|---------|
| `DEPLOY_PORT`     | `22`         | SSH-порт сервера |
| `DEPLOY_ARCH`     | `amd64`      | Архитектура сервера: `amd64` или `arm64` |
| `TILE_USER_AGENT` | `race-bot/1.0` | User-Agent для тайлов OSM — укажите контакт, напр. `race-bot/1.0 (you@example.com)` |
| `TILE_URL`        | тайлы OSM    | Шаблон URL тайлов с `{z}/{x}/{y}` |
| `TZ`              | `Europe/Moscow` | Часовой пояс дат в `/races` |
| `IDLE_TIMEOUT`    | `2h`         | Когда закрывать бессрочную трансляцию без обновлений |
| `MAX_ACCURACY_M`  | `100`        | Отбрасывать точки с худшей точностью, м |
| `MAX_SPEED_KMH`   | `120`        | Скорость, выше которой точка считается сбоем GPS |

Через `gh` CLI:

```sh
gh api -X PUT repos/{owner}/{repo}/environments/production
gh secret set TELEGRAM_TOKEN     --env production            # вставить токен
gh secret set DEPLOY_HOST        --env production --body "1.2.3.4"
gh secret set DEPLOY_USER        --env production --body "racebot"
gh secret set DEPLOY_SSH_KEY     --env production < race-bot-deploy
gh secret set DEPLOY_KNOWN_HOSTS --env production < known_hosts
gh variable set TILE_USER_AGENT  --env production --body "race-bot/1.0 (you@example.com)"
```

После этого приватный ключ `race-bot-deploy` с машины можно удалить.

## Деплой с локальной машины

Тот же скрипт:

```sh
DEPLOY_HOST=1.2.3.4 DEPLOY_USER=racebot TELEGRAM_TOKEN=... \
DEPLOY_SSH_KEY_FILE=./race-bot-deploy ./deploy/deploy.sh
```

## Обслуживание на сервере

```sh
ssh racebot@<сервер>
systemctl --user status race-bot
journalctl --user -u race-bot -f            # логи
# откат на предыдущую версию:
cd ~/race-bot && mv race-bot.prev race-bot && systemctl --user restart race-bot
# бэкап базы (безопасно на ходу):
sqlite3 ~/race-bot/data/bot.db ".backup bot-$(date +%F).db"
```

> Бот использует long polling, поэтому одновременно может работать только **один** экземпляр с этим токеном.
> Не запускайте его локально с боевым токеном, пока работает сервер, — заведите для разработки отдельного бота.

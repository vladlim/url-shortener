# Сервис сокращения ссылок

[![CI](https://github.com/vladlim/url-shortener/actions/workflows/ci.yaml/badge.svg?branch=main)](https://github.com/vladlim/url-shortener/actions/workflows/ci.yaml?query=branch%3Amain)

Go, Chi, PostgreSQL и Flyway. Мониторинг — Prometheus и Grafana.

## Запуск

Из корня проекта:

```sh
cp .env.example .env
# При необходимости измените пароли и порты в .env.
docker compose up --build -d
```

PostgreSQL запускается первым, затем Flyway применяет миграции, после чего
стартует приложение. При ошибке миграции приложение не запускается.

Адреса при стандартных настройках:

| Компонент | Адрес |
|---|---|
| API | http://localhost:8082 |
| Готовность приложения и БД | http://localhost:8082/ready |
| Метрики | http://localhost:8082/metrics |
| Prometheus | http://localhost:9090 |
| Дашборд Grafana | http://localhost:3000/d/url-shortener |

Логин и пароль Grafana задаются в `.env`; в примере — `admin` / `grafana-local`.
Если порт БД `5432` занят, измените `POSTGRES_PORT`, например на `15432`.

Остановка с сохранением данных:

```sh
docker compose down
```

## Конфигурация

| Источник | Настройки |
|---|---|
| `configs/config.yaml` | Таймауты HTTP, TTL ссылок, параметры очистки |
| Окружение приложения | `APP_ENV`, `HTTP_ADDR`, `DB_*`, `SHORTENER_HOST` |
| `.env` | Параметры локального Compose: БД, порты, вход в Grafana |

Go-приложение не читает `.env`: Compose передаёт значения через окружение.
Обязательные переменные перечислены в `.env.example` и `internal/config/dictionary.go`.
При изменении `HTTP_PORT` обновите также `SHORTENER_HOST`.

Данные PostgreSQL, Prometheus и Grafana сохраняются в отдельных томах.
Изменение пароля в `.env` не меняет уже созданные учётные записи PostgreSQL и Grafana.

## API

Создание ссылки:

```sh
curl -i http://localhost:8082/urls \
  -H 'Content-Type: application/json' \
  -d '{"client_id":"user-42","external_id":"message-123","url":"https://example.com/article"}'
```

Ответ содержит `code`, `short_url`, `original_url`, `created_at` и `expires_at`.

| Запрос | Результат |
|---|---|
| `POST /urls`, новая пара идентификаторов | `201`, новая ссылка |
| Та же активная пара и тот же URL | `200`, прежняя ссылка и TTL |
| Та же активная пара, другой URL | `409` |
| Истёкшая пара | `201`, новая ссылка; допускается другой URL |
| `GET /{code}`, `HEAD /{code}` | `302`, исходный URL в `Location` |
| Отсутствующий или истёкший код | `404` |
| `GET /health` | Проверка HTTP-процесса |
| `GET /ready` | Проверка соединения с БД |

`client_id` и `external_id` — непустые строки до 256 байт каждая.
URL — абсолютный HTTP(S)-адрес без логина и пароля, до 8192 байт.
Лимит JSON-тела — 16 КиБ; неизвестные поля запрещены.

TTL задаётся сервером и проверяется при каждом переходе. Фоновая очистка удаляет
истёкшие записи порциями. Параллельное создание одной пары защищено транзакцией
и блокировкой. Авторизация пока не реализована.

## Миграции

SQL-файлы лежат в `migrations/`: `V1__create_urls.sql`, `V2__description.sql` и далее.
Применённые миграции не редактируют — изменения добавляют новым файлом.

```sh
docker compose run --rm flyway migrate
docker compose run --rm flyway info
```

Flyway хранит версии и контрольные суммы в `flyway_schema_history`.

## Тесты и CI

Без БД — интеграционные тесты будут пропущены:

```sh
go test -race ./...
go vet ./...
```

Полный набор с отдельным PostgreSQL:

```sh
docker compose --profile test run --build --rm tests
docker compose --profile test stop postgres-test
```

Тесты используют отдельную БД и временные схемы. Для запуска интеграционных тестов
с хоста задайте `TEST_DATABASE_URL` с адресом тестовой БД.

Проверка запущенного API:

```sh
python3 scripts/smoke.py
```

GitHub Actions (`.github/workflows/ci.yaml`) на PR и push в `main` проверяет
форматирование, `go vet`, тесты с `-race`, конфигурацию Prometheus и запуск стека
с Flyway. CD не настроен.

## Мониторинг

Prometheus собирает `/metrics` каждые 10 секунд и хранит историю 15 дней.
Grafana автоматически загружает источник данных и дашборд из `monitoring/`.

Собираются число запросов, длительность, активные запросы, метрики Go и процесса.
Дашборд показывает RPS, p95, статусы, долю 5xx, результаты создания ссылок,
редиректы, память и CPU. Метки используют шаблоны маршрутов, без кодов ссылок
и пользовательских ID. Служебные ручки исключены из прикладных счётчиков.

## Структура

```text
cmd/                           сборка зависимостей и запуск
internal/handler/              HTTP-обработчики
internal/services/shortener/   бизнес-логика
internal/storage/postgres/     хранилище
internal/domain/               модель и ошибки
internal/worker/               очистка истёкших ссылок
internal/metrics/              метрики Prometheus
internal/config/               конфигурация
migrations/                    SQL-миграции
monitoring/                    настройки Prometheus и Grafana
```

Подробный разбор: [архитектура и запуск](docs/architecture.md).

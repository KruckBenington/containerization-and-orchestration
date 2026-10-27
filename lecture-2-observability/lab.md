# Лаба 2 — Мониторинг сервиса: метрики, логи, трейсы

## Часть 0 — Свой сервис

В качестве целевого приложения используется веб-сервис `api` на Go, предоставляющий HTTP-эндпоинты для проверки состояния и имитации сбоев:
* `GET /health` — проверка жизнеспособности (возвращает `200 OK`).
* `GET /fail` — имитация сбоя бизнес-логики (возвращает `500 Internal Server Error`).
* `GET /slow` — искусственная задержка ответа на 1–3 секунды для проверки трейсинга и перцентилей задержки ($p95$).
* `GET /load` — генерация фонового трафика.
* `GET /metrics` — выгрузка метрик в формате Prometheus (`http_requests_total`, `http_errors_total`, `http_request_duration_seconds`).

---

### 1. Сборка контейнера (Dockerfile)

Для минимизации размера и изоляции рантайма я использовал Multi-stage сборку:
* **Этап 1 (builder):** компиляция статического бинарника на базе `golang:1.22-alpine` с флагами `CGO_ENABLED=0` и вырезанной отладочной таблицей (`-ldflags="-w -s"`).
* **Этап 2 (финал):** образ на базе минимального `alpine:3.19`, куда копируется только готовый скомпилированный бинарник `/api` и корневые сертификаты. Итоговый размер образа составил ~15 МБ.

Сборка и загрузка образа в локальный кластер:
```bash
docker build -t api:latest .
minikube image load api:latest

2. Манифесты Kubernetes

Сервис развёрнут в отдельном пространстве имён monitoring двумя независимыми манифестами:

    deployment.yaml (Рабочая нагрузка):

        Запускает 1 реплику пода с образом api:latest (imagePullPolicy: IfNotPresent).

        В блоке annotations прописаны служебные метки для Prometheus Service Discovery: prometheus.io/scrape: "true", prometheus.io/port: "8080", prometheus.io/path: "/metrics".

        Передаёт переменную окружения OTEL_EXPORTER_OTLP_ENDPOINT со значением jaeger:4317 для отправки трассировок.

    service.yaml (Сетевой доступ):

        Создаёт внутрикластерный сервис типа ClusterIP с селектором app: api.

        Маршрутизирует входящий трафик с виртуального порта :8080 на контейнерный порт :8080.

3. Проверка запуска
Bash

# Применение манифестов
kubectl apply -f deployment.yaml
kubectl apply -f service.yaml

# Проверка статуса пода
kubectl rollout status deployment/api -n monitoring

# Проверка эндпоинтов через port-forward
kubectl port-forward svc/api 8080:8080 -n monitoring &
curl http://localhost:8080/health
curl http://localhost:8080/metrics

Под успешно перешёл в статус Running, ручка /health отдала ответ ok, а эндпоинт /metrics начал экспортировать системные и прикладные метрики.

## Часть 1 — Метрики (Prometheus + Grafana)


В этой части я настроил сбор метрик со своего сервиса `api` через Prometheus и вывел графики в Grafana по методу RED[cite: 1].

### Что я сделал:
1. **Поднял Prometheus через Helm**: он автоматически нашел мой под `api` через Service Discovery, потому что я прописал в манифесте деплоймента (`deployment.yaml`) в блоке `spec.template.metadata.annotations` аннотации:
   * `prometheus.io/scrape: "true"` — разрешает сбор метрик с пода;
   * `prometheus.io/port: "8080"` — указывает порт сервиса;
   * `prometheus.io/path: "/metrics"` — указывает путь до эндпоинта с метриками[cite: 1].
   Prometheus сам по таймеру дергает эндпоинт `/metrics` и складывает цифры к себе в базу[cite: 1]. При установке чарта я отключил постоянные диски флагом `server.persistentVolume.enabled=false`, чтобы данные писались во временную память пода, а под не вис в статусе `Pending` из-за проблем с монтированием дисков в Minikube.

2. **Развернул Grafana через Helm**: при установке передал флаг `persistence.enabled=false`, чтобы не выделять под нее постоянный том на диске, и `adminPassword="admin"`. Подключил к ней Prometheus в качестве источника данных (Data Source) по внутреннему адресу кластера `http://prometheus-server:80` в namespace `monitoring`[cite: 1].

3. **Собрал RED-дашборд из трех графиков**:
   * **Rate (RPS)**: `sum(rate(http_requests_total[5m]))` — показывает скорость входящих запросов в секунду[cite: 1].
   * **Errors (%)**: `(sum(rate(http_errors_total[5m])) / sum(rate(http_requests_total[5m])) * 100) or vector(0)` — считает процент пятисоток от общего числа запросов[cite: 1]. Конструкция `or vector(0)` нужна, чтобы при отсутствии трафика (деление 0 на 0) или пока ручка `/fail` еще ни разу не вызывалась, график показывал честный 0, а не падал в `No data`[cite: 1].
   * **Duration (p95)**: `histogram_quantile(0.95, sum by (le) (rate(http_request_duration_seconds_bucket[5m]))) or vector(0)` — показывает задержку ответа для 95% пользователей (в это время уложились 95% запросов)[cite: 1]. Здесь `or vector(0)` также удерживает график на нуле, когда запросов нет и квантиль не из чего рассчитать[cite: 1].

> **Важный момент по PromQL:** В формулах я поставил окно `[5m]`, потому что на `[1m]` Grafana часто выдавала `No data`. Функция `rate()` считает производную и требует минимум две точки сбора за выбранное окно[cite: 1]. Так как Prometheus опрашивает сервис раз в 15–30 секунд, минутного окна иногда банально не хватает для расчета.

### Проверка:
Я покрутил ручки сервиса через `curl`:
- Запрос на `/load` вызвал подъем графика Rate[cite: 1].
- Запросы на `/fail` дали скачок на панели ошибок Errors[cite: 1].
- Запрос на `/slow` уронил время ответа на панели Duration до 1–3 секунд[cite: 1].


## Часть 2 — Логи (Loki + Grafana)

## Часть 2 — Сбор логов (Loki + Promtail)

В этой части я настраивал централизованный сбор логов с сервиса `api` через связку Loki и Promtail в пространстве имён `monitoring`, подключил базу к Grafana и проверил фиксацию ошибок приложения через LogQL.

---

### 1. Архитектура доставки логов (Push-модель)

В отличие от Prometheus (где сервер сам опрашивает сервис по HTTP pull), логи работают по push-модели:
1. Сервис `api` ничего не знает про мониторинг и просто выводит сообщения и ошибки в стандартные потоки `stdout`/`stderr`.
2. Kubernetes через рантайм `containerd` складывает этот вывод в системные лог-файлы на ноде по пути `/var/log/pods`.
3. Агент **Promtail**, запущенный как `DaemonSet` на ноде, следит за файлами в этой директории и сам пушит новые строки пачками в базу **Loki** по HTTP-адресу `http://loki:3100/loki/api/v1/push`.
4. **Grafana** подключается к Loki как к источнику данных и запрашивает логи через LogQL[cite: 1].

---

### 2. Подготовка манифестов и развёртывание

Все файлы конфигураций я вынес в директорию `helm/` без комментариев внутри файлов.

#### 2.1. Развёртывание Loki (`helm/loki-values.yaml`)
Так как чарт Loki по умолчанию рассчитан на распределённый кластер из десятка подов и работу с S3, для локального Minikube я перевёл его в монолитный режим без постоянных дисков:

```yaml
deploymentMode: SingleBinary

singleBinary:
  replicas: 1
  persistence:
    enabled: false
  extraVolumes:
    - name: storage
      emptyDir: {}
  extraVolumeMounts:
    - name: storage
      mountPath: /var/loki

backend:
  replicas: 0
read:
  replicas: 0
write:
  replicas: 0

loki:
  useTestSchema: true
  auth_enabled: false
  commonConfig:
    replication_factor: 1
  storage:
    type: filesystem
  ruler:
    enabled: false

chunksCache:
  enabled: false
resultsCache:
  enabled: false

При первой установке я столкнулся с несколькими ошибками:

    Чарт выдал ошибку валидатора You have more than zero replicas configured for both the single binary and simple scalable targets. Чтобы запустить монолит SingleBinary, пришлось явно прописать replicas: 0 для распределённых компонентов read, write и backend.

    Следом чарт потребовал схему хранения (schema_config). Для локального теста я включил встроенную готовую схему параметром loki.useTestSchema: true.

    После применения манифеста под loki-0 упал в CrashLoopBackOff. В kubectl logs была ошибка mkdir /var/loki: read-only file system от модуля ruler-storage. Из-за отключения постоянных дисков файловая система заблокировалась на запись. Чтобы это исправить, я отключил модуль ruler (ruler.enabled: false) и смонтировал временный каталог emptyDir в /var/loki.

Команда установки:
Bash

helm upgrade --install loki grafana/loki -n monitoring -f helm/loki-values.yaml

После правок под loki-0 успешно перешёл в статус Running.
2.2. Развёртывание Promtail (helm/promtail-values.yaml)

Для Promtail чарт сам находит системные папки /var/log/pods на ноде, поэтому в манифесте я указал только внутрикластерный адрес сервиса Loki:
YAML

config:
  clients:
    - url: http://loki:3100/loki/api/v1/push

Команда установки:
Bash

helm upgrade --install promtail grafana/promtail -n monitoring -f helm/promtail-values.yaml

Агент успешно поднялся в виде DaemonSet.
3. Подключение к Grafana и проверка

    В веб-интерфейсе Grafana я зашёл в Data Sources, выбрал Loki и указал адрес http://loki:3100[cite: 1]. Проверка соединения подтвердила доступность сервиса[cite: 1].

    Для проверки сквозной цепочки логов я отправил запрос на сбойный эндпоинт приложения:
    Bash

curl http://localhost:8080/fail

В Grafana открыл раздел Explore, выбрал источник Loki и выполнил запрос[cite: 1]:
Фрагмент кода

{app="api"} |= "fail"

В окне сразу появилась строка лога с ошибкой /fail, вычитанная Promtail с ноды и сохранённая в Loki[cite: 1]. Сквозной сбор логов работает в реальном времени[cite: 1].



## Часть 3 — Трейсы (OpenTelemetry + Jaeger)

В этой части я внедрил распределённую трассировку в сервис `api` с помощью OpenTelemetry SDK, развернул Jaeger в пространстве имён `monitoring`, связал его с сервисом по протоколу OTLP и проверил сквозную связку «логи $\rightarrow$ трейсы» по общему `trace_id`.

---

### 1. Архитектура трассировки сервиса

В коде сервиса `main.go` я использовал библиотеки OpenTelemetry для Go (`go.opentelemetry.io/otel`).

Что именно было реализовано:
1. **Инициализация экспортера (`initTracer`)**:
   Сервис читает сетевой адрес из переменной окружения `OTEL_EXPORTER_OTLP_ENDPOINT`. Если переменная не задана, по умолчанию используется `jaeger:4317`. Экспортер упаковывает сформированные спаны в стандартный формат OTLP и пачками отправляет их по gRPC-соединению в Jaeger без использования TLS (`insecure.NewCredentials()`).
2. **Корневой спан на каждом эндпоинте**:
   При каждом входящем HTTP-запросе (`/health`, `/fail`, `/slow`, `/load`) вызывается `tracer.Start(r.Context(), path)`. Создаётся корневой спан, который засекает общее время обработки вызова клиентом и генерирует уникальный 128-битный `trace_id`.
3. **Вложенный спан `slow-op` (для `/slow`)**:
   Чтобы в веб-интерфейсе было чётко видно, на какую именно операцию уходит время, я выделил искусственную задержку в отдельный дочерний спан:
   ```go
   ctx, span := tracer.Start(r.Context(), "/slow")
   defer span.End()

   _, childSpan := tracer.Start(ctx, "slow-op")
   sleepDur := time.Duration(1000+rand.Intn(2000)) * time.Millisecond
   time.Sleep(sleepDur)
   childSpan.End()

В результате в Jaeger формируется визуальный водопад вызовов, где видно, что из суммарных ~2 секунд запроса почти 100% времени пришлось на операцию slow-op.
4. Маркировка ошибок (для /fail):
В обработчике сбойного эндпоинта спану явно проставляется статус ошибки:
Go

span.SetStatus(codes.Error, "fail endpoint triggered")

Благодаря этому Jaeger автоматически подсвечивает трейс красным маркером и выставляет атрибут error=true.
5. Сквозная связка с логами:
Из контекста текущего спана я извлекаю строковый trace_id (span.SpanContext().TraceID().String()) и внедряю его в JSON-лог каждого входящего запроса:
JSON

{"time":"...","level":"error","msg":"internal server failure triggered","path":"/fail","status":500,"trace_id":"4bc02152b0937e78..."}

Это позволяет мгновенно переходить от строки с ошибкой в Loki к детальному графику вызова в Jaeger.
2. Развёртывание Jaeger в Kubernetes

Для локального кластера Minikube полномасштабная установка Jaeger с базами данных Cassandra или Elasticsearch избыточна. Поэтому я развернул Jaeger в режиме allInOne, где коллектор, память и веб-интерфейс упакованы в один монолитный контейнер.

Файл конфигурации значений helm/jaeger-values.yaml:
YAML

allInOne:
  enabled: true
provisionDataStore:
  cassandra: false
storage:
  type: memory
agent:
  enabled: false
collector:
  enabled: false
query:
  enabled: false

Команды развёртывания:
Bash

# Добавление репозитория чартов
helm repo add jaegertracing [https://jaegertracing.github.io/helm-charts](https://jaegertracing.github.io/helm-charts)
helm repo update

# Установка Jaeger в namespace monitoring
helm upgrade --install jaeger jaegertracing/jaeger -n monitoring -f helm/jaeger-values.yaml

После старта пода я прописал деплойменту api внутрикластерный адрес сервиса Jaeger:
Bash

kubectl set env deployment/api -n monitoring OTEL_EXPORTER_OTLP_ENDPOINT="jaeger:4317"
kubectl rollout status deployment/api -n monitoring

Поскольку сервис jaeger и приложение api работают в одном пространстве имён monitoring, CoreDNS резолвит имя jaeger:4317 напрямую на внутренний ClusterIP сервиса.
3. Проверка и визуализация в Jaeger UI

Для доступа к веб-панели я пробросил локальный порт хоста на порт сервиса Jaeger:
Bash

kubectl port-forward -n monitoring svc/jaeger 16686:16686

После открытия http://localhost:16686 я сгенерировал тестовые запросы:
Bash

curl -i http://localhost:8080/slow
curl -i http://localhost:8080/fail

Результаты проверок (скриншоты):

    Водопад задержки (GET /slow):
    В интерфейсе Jaeger я выбрал сервис api и открыл трейс вызова /slow. На диаграмме отображается родительский спан /slow суммарной длительностью 1.9s, а внутри него — вложенный дочерний спан slow-op, стартовавший через 3µs и занявший ровно те же 1.9s. Это доказывает, что трассировка точно зафиксировала искусственную паузу, а не задержку сетевого стека или планировщика.

    Ошибочный спан (GET /fail):
    В списке трейсов запрос к /fail отображается с красным восклицательным знаком. В деталях спана установлены поля otel.status_code = ERROR, error = true и описание fail endpoint triggered.

    Сквозной переход из лога в трейс по trace_id:
    В Grafana Explore (источник Loki) я запросил логи приложения по фильтру {app="api"} |= "internal server failure triggered". Из найденной JSON-записи скопировал значение trace_id. Затем вставил его в поисковую строку в Jaeger UI — Jaeger моментально открыл тот самый сбойный трейс, подтвердив сквозную интеграцию логов и распределённой трассировки.


---

## Часть 4 — Алерты (Alertmanager + Karma)

В этой части работы реализована отказоустойчивая система мониторинга состояния сервиса `api` и оповещения об авариях в кластере Kubernetes: от вычисления пороговых условий на сырых метриках до диспетчеризации инцидентов и их визуализации в специализированном интерфейсе дежурного инженера.

---

## 1. Архитектура и поток данных

Вся подсистема развёрнута в пространстве имён `monitoring`. Информационный поток устроен по трёхзвенной схеме:

$$\text{Pod [api]} \xrightarrow[\text{scrape}]{\text{HTTP}} \text{Prometheus} \xrightarrow[\text{eval / firing}]{\text{HTTP POST API v2}} \text{Alertmanager} \xleftarrow[\text{REST poll}]{\text{HTTP GET}} \text{Karma}$$

1. **Prometheus Server:** по таймеру скрейпит эндпоинт `:8080/metrics` пода `api`, сохраняет временные ряды в TSDB и с периодичностью evaluation interval вычисляет PromQL-выражения alerting rules.
2. **Alertmanager:** принимает активные алерты от Prometheus, выполняет их валидацию, группировку по меткам, подавление дублей (deduplication) и управление сайленсами (silences).
3. **Karma Dashboard:** не генерирует алерты самостоятельно, а выступает агрегирующим UI для дежурной смены. Панель непрерывно опрашивает Alertmanager по API, фильтрует шум и отображает сгруппированные карточки инцидентов.

---

## 2. Манифесты и детальный построчный разбор конфигураций

Все конфигурации управляются через Helm с принципом разделения ответственности. Для обновления Prometheus без перезаписи системных настроек чарта используется отдельный файл переопределений `helm/prometheus-rules.yaml`.

### 2.1. Конфигурация Prometheus Rules (`helm/prometheus-rules.yaml`)

```yaml
server:
  alertmanagers:
    - static_configs:
        - targets:
            - alertmanager:9093

serverFiles:
  alerting_rules.yml:
    groups:
      - name: api-alerts
        rules:
          - alert: HighHttpErrorRate
            expr: (sum(rate(http_errors_total[2m])) / sum(rate(http_requests_total[2m])) * 100) > 10
            for: 30s
            labels:
              severity: critical
            annotations:
              summary: "High HTTP 5xx error rate on api"
              description: "5xx error rate exceeds 10% for the last 2 minutes"

          - alert: HighLatencyP95
            expr: histogram_quantile(0.95, sum by (le) (rate(http_request_duration_seconds_bucket[2m]))) > 1.0
            for: 30s
            labels:
              severity: critical
            annotations:
              summary: "High latency p95"
              description: "95% of requests take more than 1 second over 2 minutes"

          - alert: ServiceDown
            expr: up{app="api"} == 0 or absent(up{app="api"}) == 1
            for: 30s
            labels:
              severity: critical
            annotations:
              summary: "Service api is down"
              description: "Prometheus cannot scrape metrics from api pod"

Построчный разбор манифеста helm/prometheus-rules.yaml:

    server: — корневая секция официального чарта Prometheus (prometheus-community/prometheus), управляющая Deployment и конфигурацией пода prometheus-server.

    server.alertmanagers: — блок, который Helm транслирует в секцию alerting.alertmanagers рабочего файла prometheus.yml.

        static_configs.targets: ['alertmanager:9093'] — жестко задает внутрикластерный DNS-эндпоинт сервиса Alertmanager. В Kubernetes имя сервиса alertmanager в том же namespace резолвится CoreDNS в ClusterIP, что исключает необходимость сложного Service Discovery для самого менеджера алертов.

    serverFiles: — специальный ключ Helm-чарта. Все вложенные блоки чарт автоматически упаковывает в Kubernetes ConfigMap и монтирует как файлы в файловую систему контейнера Prometheus (/etc/config/..).

    serverFiles.alerting_rules.yml: — имя генерируемого файла с правилами.

        groups: — корневой список групп правил согласно спецификации Prometheus Rule Groups.

        name: api-alerts — логическое имя группы правил. Правила внутри одной группы вычисляются строго последовательно в рамках одного потока, что гарантирует атомарность состояния.

    rules: — список описания конкретных триггеров:

        alert: <Name> — уникальное имя правила, которое автоматически преобразуется в главный служебный лейбл {alertname="<Name>"}.

        expr: — PromQL-выражение. Если выражение возвращает вектор с одной или несколькими временными сериями, условие считается выполненным (алерт активируется).

        for: 30s — временной гистерезис. Условие expr должно непрерывно возвращать true в течение 30 секунд. В этот период алерт находится в статусе Pending (внутреннее состояние Prometheus). Лишь по истечении 30 секунд статус переключается на Firing, и Prometheus физически отправляет HTTP-запрос с пейлоадом в Alertmanager. Это предотвращает ложные тревоги от единичных секундных флуктуаций (flapping).

        labels: — системные метки инцидента. Используются механизмами роутинга Alertmanager для фильтрации, пересылки в чаты (Slack/Telegram) и дедупликации. severity: critical задает наивысший приоритет аварии.

        annotations: — текстовые метаданные для инженера (summary, description). Не влияют на идентификатор и группировку алерта в Alertmanager, но отображаются в UI Karma для быстрого траблшутинга.

2.2. Манифест развёртывания Karma

Karma развернута через Helm-чарт с передачей адреса источника через values-параметры:
YAML

env:
  - name: ALERTMANAGER_URI
    value: "http://alertmanager:9093"

Разбор параметров Karma:

    ALERTMANAGER_URI — переменная окружения демона Karma, задающая точку опроса API Alertmanager. Так как оба компонента находятся в namespace monitoring, обращение идет по короткому имени Kubernetes-сервиса alertmanager и стандартному порту 9093.

    Karma работает в режиме Read-Only poll: с интервалом по умолчанию в 30 секунд она вызывает эндпоинт /api/v2/alerts Alertmanager'а, забирает сырой JSON с активными инцидентами, группирует их по селекторам и рендерит веб-интерфейс.

3. Обоснование выбора и математика PromQL-метрик

Все три правила спроектированы по принципам RED-методологии (Rate, Errors, Duration) и мониторинга доступности сервиса (Liveness):
1. HighHttpErrorRate (Ошибки 5xx)

    Формула: (sum(rate(http_errors_total[2m])) / sum(rate(http_requests_total[2m])) * 100) > 10

    Обоснование математики:

        Абсолютное количество ошибок не отражает масштаб катастрофы (100 ошибок на 100 rps — 100% отказ, 100 ошибок на 100 000 rps — допустимый шум в 0.1%).

        Функция rate(...[2m]) считает производную (скорость прироста счетчика в секунду) на скользящем 2-минутном окне. Окно в 2 минуты гарантирует, что Prometheus накопит достаточное количество точек сбора (scrape samples) при стандартном интервале скрейпа 15–30 секунд.

        Деление rate(http_errors_total) на rate(http_requests_total) нормирует значение и возвращает точный процент сбойных ответов относительно всего входящего трафика.

2. HighLatencyP95 (Задержка ответов p95)

    Формула: histogram_quantile(0.95, sum by (le) (rate(http_request_duration_seconds_bucket[2m]))) > 1.0

    Обоснование математики:

        Среднее арифметическое время ответа скрывает проблему деградации из-за «размытия» быстрыми запросами.

        Перцентиль

p95 фиксирует границу: 95% пользователей получают ответ быстрее этого значения, а худшие 5% — дольше.

Функция rate(..._bucket[2m]) рассчитывает частоту попадания запросов в корзины гистограммы за 2 минуты, а sum by (le) агрегирует эти корзины по всем подам сервиса.

Превышение порога 1.0 секунда для

        p95 означает системный затык сервиса (троттлинг CPU, медленные блокирующие вызовы или деградацию сети).

3. ServiceDown (Отказ экземпляра приложения)

    Формула: up{app="api"} == 0 or absent(up{app="api"}) == 1

    Обоснование математики:

        Селектор app="api" нацелен строго на под сервиса api (через лейбл пода в Deployment) и игнорирует системные джобы Prometheus.

        Первая часть up{app="api"} == 0 ловит ситуацию, когда под запущен, но порт :8080 не отвечает, и скрейпер получает Network Timeout / Connection Refused.

        Вторая часть absent(up{app="api"}) == 1 критически важна: если под полностью удален, упал по OOMKilled или количество реплик смасштабировано в 0, метрика up перестает существовать в базе TSDB. Выражение up == 0 в таком случае вернуло бы пустоту (no data), и алерт бы не сработал. Функция absent() проверяет факт отсутствия временного ряда и возвращает 1, гарантируя срабатывание алерта даже при полном уничтожении пода.

4. Команды развёртывания и управления

Обновление конфигурации Prometheus выполнено через утилиту helm:
Bash

# Применение правил с сохранением уже существующих параметров пода (память, сторадж, скрейпы)
helm upgrade --install prometheus prometheus-community/prometheus \
  -n monitoring \
  -f helm/prometheus-rules.yaml \
  --reuse-values

# Ожидание плавного перезапуска пода с новым ConfigMap
kubectl rollout status deployment/prometheus-server -n monitoring

    Флаг --reuse-values сообщает Helm взять текущие значения установленного релиза и точечно накатить поверх только то, что переопределено в helm/prometheus-rules.yaml (секцию правил и адрес Alertmanager), не сбрасывая остальные системные настройки чарта.

Для доступа к дашборду Karma поднят локальный проброс портов:
Bash

kubectl port-forward -n monitoring svc/karma 8081:8080

Панель доступна по адресу http://localhost:8081.
5. Практическая верификация и стресс-тестирование

Работоспособность цепочки оповещений подтверждена эмуляцией реальных аварийных ситуаций под нагрузкой.
Тест 1. Верификация правила HighHttpErrorRate

    Провокация сбоя: сгенерирован плотный трафик на эндпоинт генерации пятисоток:
    Bash

    while true; do curl -s http://localhost:8080/fail > /dev/null; sleep 0.1; done

    Диагностика состояний:

        Счётчик http_errors_total начал линейно расти. Доля ошибок достигла 100% (> 10%).

        В веб-интерфейсе Prometheus (:9090/alerts) правило HighHttpErrorRate перешло в желтый статус Pending.

        По истечении 30 секунд непрерывной генерации статус перешел в красный Firing.

        В веб-интерфейсе Karma появилась карточка инцидента HighHttpErrorRate с меткой severity: critical.

    Восстановление: после остановки цикла запросов через Ctrl+C расчетное окно очистилось, алерт вернулся в статус Inactive и исчез из Karma.

Тест 2. Верификация правила HighLatencyP95

    Провокация сбоя: запущен цикл вызовов эндпоинта с искусственной блокирующей задержкой (1–3 секунды):
    Bash

while true; do curl -s http://localhost:8080/slow > /dev/null; sleep 0.1; done

Диагностика состояний:

    Корзины гистограммы http_request_duration_seconds_bucket заполнились значениями с задержкой выше 1с.

    Квантиль

        p95 превысил порог 1.0.

        Prometheus отсчитал таймер for: 30s и перевел алерт HighLatencyP95 в состояние Firing.

        Карточка инцидента отобразилась в интерфейсе Karma.

Тест 3. Верификация правила ServiceDown

    Провокация сбоя: сымитирована полная остановка пода масштабированием деплоймента в ноль:
    Bash

kubectl scale deployment api --replicas=0 -n monitoring

Диагностика состояний:

    Таргет исчез из базы скрейпинга, сработало условие absent(up{app="api"}) == 1.

    Правило ServiceDown отсчитало 30 секунд и перешло в Firing.

    В Karma зафиксирован критический отказ сервиса api.

Восстановление: возвращение пода в строй:
Bash

kubectl scale deployment api --replicas=1 -n monitoring

После поднятия контейнера и успешного прохождения readiness/liveness проб метрика up{app="api"} вернулась в значение 1, инцидент в Alertmanager перешел в статус Resolved, и алерт на дашборде Karma автоматически погас.

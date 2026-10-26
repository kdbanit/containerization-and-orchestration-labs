# Лаба 3. Собираем платформу для нашего сервиса

Отчёт по [Lab 3 — Kubernetes control plane](lab.md): Kyverno, Helm chart `shop`, CloudNativePG, падение control plane, мониторинг поверх стека Lab 2.

## Часть 0 — Сервисы `api` / `worker`

Сервисы на Go: `api/` и `worker/` (отдельные модули, `net/http` + `lib/pq`).

### Конфигурация

| Variable | Service | Default | Meaning |
| --- | --- | --- | --- |
| `DATABASE_URL` | api, worker | `postgres://shop:shop@postgres:5432/shop?sslmode=disable` | PostgreSQL connection string |
| `PORT` | api | `8080` | HTTP listen port |
| `HEALTH_FAIL` | api | unset / `false` | если `true`/`1`/`yes` — `/health` отвечает HTTP 503 (для Part 2) |
| `POLL_SECONDS` | worker | `2` | период опроса необработанных заказов |

### API

Создать заказ:

```console
curl -i -X POST http://localhost:8080/order \
  -H 'Content-Type: application/json' \
  -d '{"item":"book-1","quantity":2}'
```

Список заказов:

```console
curl http://localhost:8080/orders
```

Проверка health:

```console
curl -i http://localhost:8080/health
```

## Стенд

| Компонент | Конфигурация |
| --- | --- |
| Хост | Linux Ubuntu, x86_64 (`bodya-BoDE-WXX9`), Docker Engine 28.3.1 |
| Кластер | kind 0.27.0, имя `shop`, Kubernetes **v1.32.2**, 1 нода (`shop-control-plane`) |
| kubectl | v1.37.1 (клиент) |
| Helm | v3.22.1 |
| Policy engine | Kyverno chart **3.3.7**, app **v1.13.4** (Helm release `kyverno`, ns `kyverno`) |
| Postgres operator | CloudNativePG chart **0.29.1** (app 1.30.1), ns `cnpg-system` |
| Мониторинг | Lab 2 reuse в ns `monitoring`: `kube-prometheus-stack` (Prometheus/Grafana/Alertmanager), Loki, Alloy, Jaeger |

Локальный кластер подняли через kind. Нода кластера — Docker-контейнер `shop-control-plane` (образ `kindest/node:v1.32.2`), API доступен на `127.0.0.1:39049→6443`.

```console
kind create cluster --name shop
```

![Создание kind-кластера shop](screenshots/part0-kind-create.png)

После старта нода в статусе Ready; в `kube-system` крутятся etcd, apiserver, scheduler, controller-manager, CoreDNS, kindnet, kube-proxy; в `local-path-storage` — provisioner для PVC.

```console
kubectl get nodes
kubectl get pods -A
```

![Нода Ready и системные поды](screenshots/part0-nodes-and-system-pods.png)

### Структура репозитория

```text
shop/
  api/ worker/       # Go + Dockerfile
  policies/          # ClusterPolicy (+ bad/ — отвергаемые манифесты)
  charts/shop/       # Helm chart приложения (Part 2+)
  platform/          # overrides чужих chart'ов
  screenshots/       # скрины part1–part5
  README.md
```

---

## Часть 1 — Ограждения на кластер

### Выбор движка политик

Между OPA/Gatekeeper и Kyverno выбран **Kyverno**. Для лабы достаточно типовых admission-правил (limits, labels, privileged, registry) — их удобно описать обычными Kubernetes-манифестами `ClusterPolicy`, без отдельного языка Rego. OPA/Gatekeeper мощнее и гибче, но в этом контексте выглядит оверкиллом: пришлось бы учить ещё один слой поверх уже нового для нас Kubernetes.

### Установка Kyverno

Движок поставили отдельным Helm release (не vendoring chart в репо). Из‑за проблем с IPv6 на `kyverno.github.io` chart взяли из OCI:

```console
helm upgrade --install kyverno oci://ghcr.io/kyverno/charts/kyverno \
  --namespace kyverno \
  --create-namespace \
  --version 3.3.7 \
  --wait
```

Release: `kyverno`, revision `1`, status `deployed`. Chart `kyverno-3.3.7`, app `v1.13.4`.

![Установка Kyverno через Helm (OCI)](screenshots/part1-kyverno-helm-install.png)

```console
helm list -A
```

![Helm release kyverno deployed](screenshots/part1-helm-list.png)

В namespace `kyverno` работают admission-, background-, cleanup- и reports-controller (все Ready `1/1`).

```console
kubectl get pods -n kyverno
```

![Поды Kyverno Running](screenshots/part1-kyverno-pods.png)

Kyverno регистрирует CRD, в том числе `clusterpolicies.kyverno.io`. Правило — обычный объект Kubernetes (`ClusterPolicy`), проверка идёт на **admission** (третий гейт API server) до записи в etcd.

### Правила для кластера

#### 1. Обязательные CPU/memory limits — `require-resources`

Файл: [`policies/require-resources.yaml`](policies/require-resources.yaml).

`validationFailureAction: Enforce`, `background: false`, match на `Pod`.  
Паттерн требует, чтобы у каждого контейнера были заданы `resources.limits.cpu` и `resources.limits.memory` (в языке Kyverno — `"?*"`, любое непустое значение).

**Зачем:** без limits контейнер может забрать почти всю память или CPU ноды и «задушить» соседей. Limit — жёсткий потолок (на ноде это уходит в cgroup): процесс не вырастет выше заданного. Так один плохой или жадный Pod не съедает ресурсы всей машины.

Применение и статус:

```console
kubectl apply -f shop/policies/require-resources.yaml
# clusterpolicy.kyverno.io/require-resources created

kubectl get clusterpolicy
```

![Создание ClusterPolicy require-resources](screenshots/part1-policy-apply.png)

![ClusterPolicy require-resources Ready](screenshots/part1-clusterpolicies.png)

#### Проверка reject

Манифест без limits: [`policies/bad/no-limits.yaml`](policies/bad/no-limits.yaml).

```console
$ kubectl apply -f bad/no-limits.yaml
Error from server: error when creating "bad/no-limits.yaml": admission webhook "validate.kyverno.svc-fail" denied the request:

resource Pod/default/bad-no-limits was blocked due to the following policies

require-resources:
  require-cpu-memory-limits: 'validation error: Each container must set resources.limits.memory
    and resources.limits.cpu. rule require-cpu-memory-limits failed at path /spec/containers/0/resources/limits/'
```

Отказ на **admission** (третий гейт API server): Kyverno отклонил объект до записи в etcd.  
`kubectl get pod bad-no-limits` → `NotFound` — Pod в кластере не создан.

![Отклонение Pod без resource limits политикой require-resources](screenshots/part1-reject-no-limits.png)

#### 2. Запрет privileged — `disallow-privileged`

Файл: [`policies/disallow-priveleged.yaml`](policies/disallow-priveleged.yaml) (в имени файла опечатка `priveleged`; `metadata.name` в кластере — `disallow-privileged`).

**Зачем:** `securityContext.privileged: true` почти снимает изоляцию контейнера от хоста — процесс получает широкие права на ноду. Обычному приложению это не нужно; политика не даёт такому Pod’у даже попасть в etcd.

Плохой манифест (с limits, чтобы сработало именно это правило, а не `require-resources`): [`policies/bad/priveleged.yaml`](policies/bad/priveleged.yaml) / `privileged.yaml`.

```console
$ kubectl apply -f bad/privileged.yaml
Error from server: ... admission webhook "validate.kyverno.svc-fail" denied the request:
...
disallow-privileged:
  privileged-containers: 'validation error: Privileged containers are not allowed.
    rule privileged-containers failed at path /spec/containers/0/securityContext/privileged/'
```

![Создание disallow-privileged](screenshots/part1-policy-disallow-privileged.png)

![Отклонение privileged Pod](screenshots/part1-reject-privileged.png)

#### 3. Обязательные labels — `require-labels`

Файл: [`policies/require-labels.yaml`](policies/require-labels.yaml).

Требуем на Pod: `app.kubernetes.io/name`, `app.kubernetes.io/part-of`, `app.kubernetes.io/managed-by`.

**Зачем:** labels — способ помечать и находить объекты (Service → поды, мониторинг, Helm). Без общих меток непонятно, к какому приложению относится Pod, и селекторы/дашборды ломаются.

Плохой манифест без labels (но с limits): [`policies/bad/no-labels.yaml`](policies/bad/no-labels.yaml).

```console
kubectl apply -f require-labels.yaml
# clusterpolicy.kyverno.io/require-labels created
kubectl get clusterpolicy   # три политики Ready

kubectl apply -f bad/no-labels.yaml
# denied: require-labels / require-app-labels … failed at path /metadata/labels/
```

![Создание require-labels; три ClusterPolicy Ready](screenshots/part1-policy-require-labels.png)

![Отклонение Pod без обязательных labels](screenshots/part1-reject-no-labels.png)

#### 4. Только доверенный registry — `require-trusted-registry`

Файл: [`policies/require-trusted-registry.yaml`](policies/require-trusted-registry.yaml).

Разрешённые префиксы образа: `docker.io/*`, `registry.k8s.io/*`, `ghcr.io/*`, `shop/*` (локальные образы приложения после `kind load`).

**Зачем:** образ скачивается с registry. Неизвестный хост — риск запустить непроверенный/вредоносный артефакт. Список доверенных registry задаёт границу: с остального кластер просто не примет Pod.

Плохой манифест: [`policies/bad/untrusted-image.yaml`](policies/bad/untrusted-image.yaml) (`evil.example/malware:1.0`, при этом limits и labels соблюдены).

```console
kubectl apply -f require-trusted-registry.yaml
# created
kubectl apply -f bad/untrusted-image.yaml
# denied: trusted-images … failed at path /spec/containers/0/image/
```

![Создание require-trusted-registry и reject образа с evil.example](screenshots/part1-reject-untrusted-image.png)

#### 5. Запрет тега `:latest` — `disallow-latest-tag`

Файл: [`policies/disallow-latest-tag.yaml`](policies/disallow-latest-tag.yaml).

Паттерн: образ не должен заканчиваться на `:latest` (`image: "!*:latest"`).

**Зачем:** тег `latest` плавает — завтра под тем же именем может оказаться другой бинарь. Явный тег (`1.27`, `v1`) делает деплой воспроизводимым: понятно, что именно запущено.

Плохой манифест: [`policies/bad/latest-tag.yaml`](policies/bad/latest-tag.yaml).

```console
kubectl apply -f disallow-latest-tag.yaml
# created
kubectl apply -f bad/latest-tag.yaml
# denied: no-latest … failed at path /spec/containers/0/image/
```

![Создание disallow-latest-tag и reject образа :latest](screenshots/part1-reject-latest-tag.png)

### Итог Part 1

Пять ClusterPolicy в Enforce; для каждой есть отвергающий манифест в `policies/bad/`. Chart приложения (Part 2) должен сразу удовлетворять всем правилам — иначе `helm install` упрётся в admission.

Позже в правила добавили `exclude` для namespace’ов `kube-system`, `local-path-storage`, `kyverno`, `cnpg-system`: иначе Kyverno блокировал helper-Pod provisioner’а PVC (как у atwn с исключением `obs` для стека мониторинга). Это доверенная зона платформы; application namespace `shop` по-прежнему под политиками.

```console
kubectl get clusterpolicy
```

![Все пять ClusterPolicy Ready](screenshots/part1-all-clusterpolicies.png)

| # | Правило | Файл | Зачем | Скрины |
| --- | --- | --- | --- | --- |
| 1 | require cpu/memory limits | `policies/require-resources.yaml` | чтобы один Pod не съел ресурсы ноды | apply + list + reject |
| 2 | disallow privileged | `policies/disallow-priveleged.yaml` | не снимать изоляцию с хоста | apply + reject |
| 3 | require labels | `policies/require-labels.yaml` | единообразие и поиск подов | apply + reject |
| 4 | require trusted registry | `policies/require-trusted-registry.yaml` | не тянуть образы с левых registry | apply + reject |
| 5 | disallow `:latest` | `policies/disallow-latest-tag.yaml` | воспроизводимые деплои | apply + reject |

---

## Часть 2 — Helm-чарт api и worker

Chart: `charts/shop`. По умолчанию api ×3, worker ×2. В шаблоны заложены требования политик Part 1; probes `/health` у api.

### Установка чарта

Образы `shop/api:v1` и `shop/worker:v1` собраны локально и загружены в kind (`kind load`).  
StorageClass в kind называется `standard` (не `local-path`).  
Первые `helm install --wait` зависали: (1) неверный StorageClass; (2) Kyverno резал helper-Pod в `local-path-storage`. После exclude и `storageClass: standard` release встал:

```console
helm upgrade --install shop ./charts/shop -n shop --wait
kubectl get pods,pvc,cluster -n shop
# api 1/1 ×3, worker 1/1 ×2, postgres 1/1, Cluster healthy, PVC Bound
```

![Установка shop: pods Ready, PVC Bound, Cluster healthy](screenshots/part2-install.png)

Проверка через `kubectl port-forward svc/shop-api 8080:80`:

```console
curl -i http://127.0.0.1:8080/health
# HTTP/1.1 200 OK
# {"status":"ok"}
```

![API /health отвечает ok](screenshots/part2-health.png)

Рестарты api/worker в первые минуты — ожидание Postgres (init); после healthy Cluster поды стабилизировались.

### Reconciliation после удаления Pod

Удалили один Pod api вручную (`shop-api-…-g4qlg`). Поле `spec.replicas` у Deployment не меняли, поэтому контроллер ReplicaSet увидел расхождение desired/actual (нужно 3, стало 2) и создал замену с новым суффиксом (`…-rwqml`), AGE ≈ секунды, Restarts 0. Тот же `pod-template-hash` (`85d498bc4c`) — шаблон Pod не менялся, это не новый rollout.

Управляем желаемым состоянием (Deployment), а не отдельными Pod’ами: удаление Pod временное, удаление Deployment убрало бы сервис навсегда.

![После delete pod api ReplicaSet восстановил третью реплику](screenshots/part2-reconciliation.png)

### Масштабирование через Helm

В `values.yaml` сменили `api.replicaCount` с `3` на `4`, затем `helm upgrade shop ./charts/shop -n shop --wait` (revision **2**).  
Deployment получил новое желаемое число реплик; появился четвёртый Pod. Service менять не нужно — Ready Pod подхватывается по labels.

![Масштабирование api до 4/4](screenshots/part2-scaling.png)

### Rolling update

Собрали `shop/api:v2`, загрузили в kind, в `values` сменили образ, в Deployment задали `maxUnavailable: 0` / `maxSurge: 1`.  
`helm upgrade` (revision **3**) прокатил поды по одному: новый ReplicaSet `75cb5dcf4b` вырос до 4, старый `85d498bc4c` — до 0. Пока идёт rollout, Service продолжает отдавать трафик на Ready-поды.

![Rolling update / состояние после здорового v2](screenshots/part2-rolling-update.png)

### Зависший rollout и rollback

Выставили `api.healthFail: true` и снова `helm upgrade`. Новый ReplicaSet (`5495554649`) поднял один Pod (`maxSurge: 1`), но readiness на `/health` не проходит (**0/1**, рестарты liveness). Из‑за `maxUnavailable: 0` старые четыре Ready-пода **не снимают** — сервис продолжает отвечать 200. Rollout «застрял» в безопасном состоянии.

![HEALTH_FAIL: новый Pod 0/1, старые 1/1 живы](screenshots/part2-failed-rollout.png)

`helm upgrade --wait` (revision **4**) завершился `context deadline exceeded` / STATUS `failed` — объекты в кластере остались, автоматического отката нет.  
В `values.yaml` вернули `healthFail: false` (rollback **не** правит файлы на диске). Затем:

```console
helm rollback shop 3 -n shop
# revision 5 = Rollback to 3
```

Сломанный ReplicaSet сведён к 0, снова 4 Ready-пода `75cb5dcf4b` (v2), `/health` → `ok`.

![После rollback: RS 75cb5dcf4b 4/4, сломанный RS 0](screenshots/part2-rollback-state.png)

История release: 1 install → 2 scale → 3 rolling v2 → 4 failed (HEALTH_FAIL, deadline) → 5 Rollback to 3 (deployed).

![helm history shop](screenshots/part2-helm-history.png)

---

## Часть 3 — PostgreSQL через CloudNativePG

Оператор поставили **отдельно** от приложения (release `cnpg`, ns `cnpg-system`, chart `0.29.1`, app `1.30.1`).  
Первая установка без values упала на Kyverno (нет labels/limits у Deployment operator) — починили override [`platform/cnpg-values.yaml`](platform/cnpg-values.yaml).

![CloudNativePG operator Running](screenshots/part3-operator.png)

![CRD postgresql.cnpg.io зарегистрированы](screenshots/part3-crd.png)

В chart `shop` добавлены Secret учётных данных и объект `Cluster` (`templates/postgres-*.yaml`).  
Один инстанс + PVC `local-path` достаточно для проверки reconciliation.

### Reconciliation после удаления PostgreSQL Pod

Перед удалением UID пода `shop-postgres-1`:

```text
4937cebe-318b-4806-a18b-187ef5895bad
```

Обычный `kubectl delete pod` долго «висел»: у инстанса CNPG `terminationGracePeriodSeconds: 1800` (до 30 минут на graceful stop). Для лабы использовали принудительное удаление / дождались Terminating → новый цикл. Operator создал под **с тем же именем**, но другим UID:

```text
e3988298-9051-4b4d-8a0d-d8ab0af66091
```

Другой UID = новый объект Kubernetes, не restart контейнера в старом Pod.  
PVC `shop-postgres-1` остался **Bound** (тот же volume) — данные переживают пересоздание пода.

![UID до/после и PVC Bound](screenshots/part3-recreated-pod.png)

![Watch: Terminating → Pending → Running 1/1](screenshots/part3-postgres-watch.png)

### Operator vs controller-manager

**controller-manager** крутит встроенные reconciliation-циклы Kubernetes (Deployment → ReplicaSet → Pod, Service и т.д.): универсальная логика «сколько копий», без знания про Postgres.

**Operator (CloudNativePG)** — отдельный контроллер плюс CRD `Cluster`: в `spec` описано желаемое состояние БД (инстансы, storage, bootstrap), в `status` — факт (primary, Ready, phase). Operator сам создаёт Pod/PVC/Service, делает initdb и поднимает инстанс после удаления пода. Это та же идея reconciliation, но с экспертизой «как правильно жить с PostgreSQL», которой у controller-manager нет.

---

## Часть 4 — Падение control plane

На kind control plane живёт в контейнере `shop-control-plane`. Чтобы уронить **только** мозг, а не всю ноду, убрали static-pod манифест API server:

```bash
docker exec shop-control-plane \
  mv /etc/kubernetes/manifests/kube-apiserver.yaml /root/kube-apiserver.yaml.bak
```

(`docker pause` на однонодовом kind замораживает и поды — для эксперимента не подходит.)

![control plane down / data plane / restore](screenshots/part4-control-plane.png)

### Что отвалилось

`kubectl` перестал достучаться до API (`context deadline exceeded` / EOF). Менять объекты нельзя: ни `helm upgrade`, ни `kubectl apply` — «дверь» в etcd закрыта.

### Что продолжило жить

Уже запущенные поды `shop-api` / `shop-worker` / `shop-postgres-1` и Service `shop-api` (ClusterIP `10.96.196.212`) продолжали отвечать. Изнутри ноды:

```text
curl http://10.96.196.212/health  →  {"status":"ok"}
```

Port-forward через API server во время аварии не поднять; локальный `curl :8080` мог работать, только если сессия PF уже была открыта *до* падения.

### Восстановление

Вернули манифест на место — kubelet снова поднял `kube-apiserver`:

```bash
docker exec shop-control-plane \
  mv /root/kube-apiserver.yaml.bak /etc/kubernetes/manifests/kube-apiserver.yaml
```

Через ~30 с: `kubectl get nodes` → Ready, `kubectl get pods -n shop` → все Running/Ready. Кластер снова управляем; data plane всё это время не «перезапускали».

**Вывод:** падение control plane замораживает *изменения* и управление; уже работающий data plane (kubelet + контейнеры + сеть сервисов) живёт сам.

---

## Часть 5 — Мониторинг и алертинг

Reuse стека Lab 2 в ns `monitoring` (`kube-prometheus-stack`, Loki, Alloy, Jaeger). В chart `shop` добавлены `ServiceMonitor` и `PrometheusRule` (label `release: monitoring`, чтобы Prometheus Operator их подхватил). У `api` v3 — RED-метрики на `/metrics`.

### Три алерта

| Alert | Что ловит | Почему важно |
| --- | --- | --- |
| `ShopApiDown` | `up{job="shop-api"} == 0` дольше 1m | scrape не проходит — сервис недоступен для мониторинга и, с высокой вероятностью, для клиентов; on-call сначала смотрит поды/Service/сеть |
| `ShopApiHighErrorRate` | доля HTTP 5xx > 10% за 2m, for 1m | пользователи получают ошибки (заказы/health); типичный триггер — деградация БД, баг, HEALTH_FAIL; чинить до эскалации SLO |
| `ShopApiHighLatency` | p95 latency > 1s за 5m, for 2m | магазин «живой», но тормозит — очередь/БД/CPU; ловим до полного отказа |

### Проверка срабатывания алерта

Гнали `GET /fail` → 503 (отдельный эндпоинт; `/health` для probes не ломали — иначе readiness и `helm --wait` зависают).  
Через ~1–2 мин `ShopApiHighErrorRate` перешёл в **FIRING** (~70% 5xx > порога 10%).

![alert firing](screenshots/part5-alert-firing.png)

### Борд в Grafana

Explore / Prometheus: Rate, Errors %, p95 Duration по `job="shop-api"` (RED).

![grafana RED](screenshots/part5-grafana-red.png)

---

## Чеклист сдачи

- [x] код `api`/`worker` + Dockerfile
- [x] бандл политик (≥5) + bad-манифесты
- [x] Helm chart `charts/shop` (+ ServiceMonitor / PrometheusRule)
- [x] README Part 1: выбор движка + обоснования правил
- [x] Part 2–5 в README + скриншоты (part0–part5)

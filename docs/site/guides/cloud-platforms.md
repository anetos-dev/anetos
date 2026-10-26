---
title: Deploy to a cloud platform
since: v0.6.0
group: "Production"
weight: 801
---

# Deploy to a cloud platform

Run the app's image on a platform that runs containers for you: Google
Cloud Run, Azure Container Apps, Amazon ECS on Fargate, DigitalOcean
App Platform, any Kubernetes cluster (GKE, EKS, AKS, DigitalOcean
Kubernetes), Fly.io or Render. Each section deploys the same app,
called `blog`: the web server, the background work (queue workers and
the scheduler), the migrations before each new version, and a managed
PostgreSQL database.

The image, the settings and the migrations are those of
[Deploy](deployment.md); read it first.

> **Note:** Cloud platforms change their services, commands and
> settings often, and we may miss a change along the way. If something
> here doesn't match what you see on a platform, please let us know by
> [opening a documentation issue](https://github.com/anetos-dev/anetos/issues/new?template=docs.yml)
> or sending a pull request ("Edit this page", below), and we'll update
> the guide. Each section links to the platform's own documentation.

## Before you start

- The project's `Dockerfile` builds the image ([Run it in a
  container](deployment.md#run-it-in-a-container)). The platforms below
  either build it from the repository or pull it from their registry:
  push it there with `docker build` and `docker push`, tagged with the
  version (`blog:1.0.0`).
- A PostgreSQL project (`anetos new --db=postgres`), or MySQL with the
  same settings. SQLite needs a disk that lasts and a single instance:
  on Fly.io, a volume does it (below); elsewhere, use a server
  ([Deploy](deployment.md)).
- A production key: `go tool anetos key:generate --show`. Keep it in
  the platform's secret store, never in the repository.

Every platform gets the same settings, set in its own way:

| Setting | On a platform |
|---|---|
| `APP_PROCESS_TYPES` | The process's role, so its command stays the image's `run`: `web` for the web server, `worker,scheduler` for the background work (see [process types](deployment.md#6-split-the-process-types-as-you-grow)) |
| `APP_KEY`, `DB_PASSWORD` | From the platform's secret store |
| `DB_HOST`, `DB_PORT`, `DB_NAME`, `DB_USER` | The managed database. TLS is on and verified for a remote host; `DB_TLS_CA` names the database's own CA, as a file or as PEM text |
| `CACHE_DRIVER`, `SESSION_DRIVER`, `QUEUE_DRIVER` | `database`, so every instance shares them |
| `APP_SHUTDOWN_TIMEOUT` | Below the time the platform waits between SIGTERM and killing the process |
| `HTTP_TRUSTED_PROXIES` or `HTTP_CLIENT_IP_HEADER` | How the app finds the visitor's IP behind the platform's proxy (rate limits, logs, the audit log) |
| `STORAGE_DRIVER` | `s3` or `gcs` for uploads: a platform's disk doesn't outlive the instance ([Store files](storage.md)) |

And each deploy does the same three things, in order:

1. Run `migrate` once, with the new image, as a one-off job or task.
2. Start the new web instances. While migrations are pending, they
   answer `/health/ready` with 503, so the platform keeps the old ones
   serving.
3. Replace the background process. Run one instance of the scheduler:
   a platform may briefly run two while it replaces one, so give
   tasks that mustn't run twice `schedule.OnOneServer()`.

The examples use placeholders in capitals (`PROJECT`, `REGION`) and
`blog.example.com`; replace them with yours.

## Google Cloud Run

The web server is a Cloud Run service, the background work a worker
pool (an instance that serves no requests), and the migrations a Cloud
Run job. The database is Cloud SQL for PostgreSQL, reached through the
Unix socket Cloud Run provides, which it encrypts: no TLS settings.

1. Create a service account for the app with the Cloud SQL Client and
   Secret Manager Secret Accessor roles, and the secrets:

   ```sh
   printf '%s' "base64:…" | gcloud secrets create blog-app-key --data-file=-
   printf '%s' "the database password" | gcloud secrets create blog-db-password --data-file=-
   ```

2. Push the image to Artifact Registry
   (`REGION-docker.pkg.dev/PROJECT/apps/blog:1.0.0`).

3. Run the migrations with a job, which `--execute-now --wait` runs at
   once and waits for: a failure stops the deploy there. The job and
   the worker pool read their settings from one file,
   `deploy/cloudrun/worker.env.yaml`:

   ```yaml
   # The worker pool's and the migration job's settings (the secrets come
   # from --set-secrets).
   APP_PROCESS_TYPES: worker,scheduler
   APP_URL: https://blog.example.com
   APP_SHUTDOWN_TIMEOUT: 8s
   DB_DRIVER: postgres
   DB_HOST: /cloudsql/PROJECT:REGION:INSTANCE
   DB_NAME: blog
   DB_USER: blog
   CACHE_DRIVER: database
   SESSION_DRIVER: database
   QUEUE_DRIVER: database
   ```

   ```sh
   gcloud run jobs deploy blog-migrate --region=REGION \
     --image=REGION-docker.pkg.dev/PROJECT/apps/blog:1.0.0 --args=migrate \
     --service-account=blog@PROJECT.iam.gserviceaccount.com \
     --set-cloudsql-instances=PROJECT:REGION:INSTANCE \
     --env-vars-file=deploy/cloudrun/worker.env.yaml \
     --set-secrets=APP_KEY=blog-app-key:latest,DB_PASSWORD=blog-db-password:latest \
     --tasks=1 --max-retries=0 --task-timeout=10m --execute-now --wait
   ```

4. Deploy the web server from `deploy/cloudrun/service.yaml`:

   ```yaml
   apiVersion: serving.knative.dev/v1
   kind: Service
   metadata:
     name: blog
     labels:
       cloud.googleapis.com/location: REGION
   spec:
     template:
       metadata:
         annotations:
           autoscaling.knative.dev/minScale: "1"
           autoscaling.knative.dev/maxScale: "10"
           run.googleapis.com/cloudsql-instances: PROJECT:REGION:INSTANCE
       spec:
         serviceAccountName: blog@PROJECT.iam.gserviceaccount.com
         containers:
           - image: REGION-docker.pkg.dev/PROJECT/apps/blog:1.0.0
             ports:
               - name: http1
                 containerPort: 8080
             env:
               - {name: APP_PROCESS_TYPES, value: web}
               - {name: APP_URL, value: "https://blog.example.com"}
               # Cloud Run kills the instance 10 seconds after SIGTERM.
               - {name: APP_SHUTDOWN_TIMEOUT, value: 8s}
               - {name: DB_DRIVER, value: postgres}
               # Cloud SQL's socket: encrypted by Cloud Run, no TLS settings.
               - {name: DB_HOST, value: /cloudsql/PROJECT:REGION:INSTANCE}
               - {name: DB_NAME, value: blog}
               - {name: DB_USER, value: blog}
               - {name: CACHE_DRIVER, value: database}
               - {name: SESSION_DRIVER, value: database}
               - {name: QUEUE_DRIVER, value: database}
               - name: DB_PASSWORD
                 valueFrom: {secretKeyRef: {name: blog-db-password, key: latest}}
               - name: APP_KEY
                 valueFrom: {secretKeyRef: {name: blog-app-key, key: latest}}
             resources:
               limits: {cpu: "1", memory: 512Mi}
             # Ready once the migrations have run: a new revision gets traffic then.
             startupProbe:
               httpGet: {path: /health/ready, port: 8080}
               periodSeconds: 2
               failureThreshold: 30
             livenessProbe:
               httpGet: {path: /health/live, port: 8080}
               periodSeconds: 10
   ```

   ```sh
   gcloud run services replace deploy/cloudrun/service.yaml --region=REGION
   gcloud run services add-iam-policy-binding blog --region=REGION \
     --member=allUsers --role=roles/run.invoker   # a public site
   ```

5. Deploy the background work as a worker pool of one instance:

   ```sh
   gcloud run worker-pools deploy blog-worker --region=REGION \
     --image=REGION-docker.pkg.dev/PROJECT/apps/blog:1.0.0 --instances=1 \
     --service-account=blog@PROJECT.iam.gserviceaccount.com \
     --add-cloudsql-instances=PROJECT:REGION:INSTANCE \
     --env-vars-file=deploy/cloudrun/worker.env.yaml \
     --set-secrets=APP_KEY=blog-app-key:latest,DB_PASSWORD=blog-db-password:latest
   ```

For each new version, change the image's tag and run steps 3 to 5.

Cloud Run gives a process 10 seconds between SIGTERM and SIGKILL,
hence `APP_SHUTDOWN_TIMEOUT=8s`: a queue job still running then is
cut off, so keep jobs short. Secrets are read when
an instance starts: after changing one, deploy again. The disk is in
memory and gone when the instance stops, so uploads need
`STORAGE_DRIVER=gcs`.

The client's IP: Cloud Run adds it to `X-Forwarded-For`, but doesn't
document the address its proxy connects from. After the first deploy,
the request log's `ip` field shows that address (the proxy's, while it
isn't trusted): put it, or its range, in `HTTP_TRUSTED_PROXIES`.

The platform's docs: [deploying a service](https://docs.cloud.google.com/run/docs/deploying),
[the service YAML](https://docs.cloud.google.com/run/docs/reference/yaml/v1),
[health checks](https://docs.cloud.google.com/run/docs/configuring/healthchecks),
[worker pools](https://docs.cloud.google.com/run/docs/deploy-worker-pools),
[jobs](https://docs.cloud.google.com/run/docs/create-jobs),
[Cloud SQL from Cloud Run](https://docs.cloud.google.com/sql/docs/postgres/connect-run),
[secrets](https://docs.cloud.google.com/run/docs/configuring/services/secrets),
[the container contract](https://docs.cloud.google.com/run/docs/container-contract).

## Azure Container Apps

The web server and the background work are two container apps in one
environment, and the migrations a container apps job started by hand.
The database is Azure Database for PostgreSQL (flexible server), whose
certificate chains to public roots: TLS is verified with no CA to give.
The secrets are in Key Vault, read by a user-assigned managed identity,
which also pulls the image from Azure Container Registry.

1. Create the identity, give it the Key Vault Secrets User role on the
   vault and AcrPull on the registry, and put the secrets in the vault
   (`app-key`, `db-password`).

2. Push the image to the registry (`blogregistry.azurecr.io/blog:1.0.0`).

3. Create the migration job from `deploy/azure/migrate.yaml`
   (`az containerapp job create -n blog-migrate -g blog --yaml
   deploy/azure/migrate.yaml`):

   ```yaml
   identity:
     type: UserAssigned
     userAssignedIdentities:
       /subscriptions/SUBSCRIPTION/resourceGroups/blog/providers/Microsoft.ManagedIdentity/userAssignedIdentities/blog: {}
   properties:
     environmentId: /subscriptions/SUBSCRIPTION/resourceGroups/blog/providers/Microsoft.App/managedEnvironments/blog
     configuration:
       triggerType: Manual
       replicaTimeout: 1800
       replicaRetryLimit: 0
       manualTriggerConfig:
         parallelism: 1
         replicaCompletionCount: 1
       registries:
         - server: blogregistry.azurecr.io
           identity: /subscriptions/SUBSCRIPTION/resourceGroups/blog/providers/Microsoft.ManagedIdentity/userAssignedIdentities/blog
       secrets:
         - name: app-key
           keyVaultUrl: https://blog-vault.vault.azure.net/secrets/app-key
           identity: /subscriptions/SUBSCRIPTION/resourceGroups/blog/providers/Microsoft.ManagedIdentity/userAssignedIdentities/blog
         - name: db-password
           keyVaultUrl: https://blog-vault.vault.azure.net/secrets/db-password
           identity: /subscriptions/SUBSCRIPTION/resourceGroups/blog/providers/Microsoft.ManagedIdentity/userAssignedIdentities/blog
     template:
       containers:
         - name: migrate
           image: blogregistry.azurecr.io/blog:1.0.0
           args: [migrate]
           resources: {cpu: 0.5, memory: 1Gi}
           env:
             - {name: DB_DRIVER, value: postgres}
             - {name: DB_HOST, value: blog-db.postgres.database.azure.com}
             - {name: DB_NAME, value: blog}
             - {name: DB_USER, value: blog}
             - {name: DB_PASSWORD, secretRef: db-password}
             - {name: APP_KEY, secretRef: app-key}
   ```

4. Create the web app from `deploy/azure/web.yaml` (`az containerapp
   create -n blog -g blog --yaml deploy/azure/web.yaml`):

   ```yaml
   identity:
     type: UserAssigned
     userAssignedIdentities:
       /subscriptions/SUBSCRIPTION/resourceGroups/blog/providers/Microsoft.ManagedIdentity/userAssignedIdentities/blog: {}
   properties:
     environmentId: /subscriptions/SUBSCRIPTION/resourceGroups/blog/providers/Microsoft.App/managedEnvironments/blog
     configuration:
       activeRevisionsMode: Single
       ingress:
         external: true
         targetPort: 8080
       registries:
         - server: blogregistry.azurecr.io
           identity: /subscriptions/SUBSCRIPTION/resourceGroups/blog/providers/Microsoft.ManagedIdentity/userAssignedIdentities/blog
       secrets:
         - name: app-key
           keyVaultUrl: https://blog-vault.vault.azure.net/secrets/app-key
           identity: /subscriptions/SUBSCRIPTION/resourceGroups/blog/providers/Microsoft.ManagedIdentity/userAssignedIdentities/blog
         - name: db-password
           keyVaultUrl: https://blog-vault.vault.azure.net/secrets/db-password
           identity: /subscriptions/SUBSCRIPTION/resourceGroups/blog/providers/Microsoft.ManagedIdentity/userAssignedIdentities/blog
     template:
       terminationGracePeriodSeconds: 40
       containers:
         - name: web
           image: blogregistry.azurecr.io/blog:1.0.0
           resources: {cpu: 0.5, memory: 1Gi}
           env:
             - {name: APP_PROCESS_TYPES, value: web}
             - {name: APP_URL, value: "https://blog.example.com"}
             - {name: DB_DRIVER, value: postgres}
             - {name: DB_HOST, value: blog-db.postgres.database.azure.com}
             - {name: DB_NAME, value: blog}
             - {name: DB_USER, value: blog}
             - {name: DB_PASSWORD, secretRef: db-password}
             - {name: APP_KEY, secretRef: app-key}
             - {name: CACHE_DRIVER, value: database}
             - {name: SESSION_DRIVER, value: database}
             - {name: QUEUE_DRIVER, value: database}
           probes:
             - type: Startup
               httpGet: {path: /health/live, port: 8080}
               periodSeconds: 2
               failureThreshold: 10
             - type: Readiness
               httpGet: {path: /health/ready, port: 8080}
               periodSeconds: 5
               failureThreshold: 2
             - type: Liveness
               httpGet: {path: /health/live, port: 8080}
               periodSeconds: 10
       scale:
         minReplicas: 1
         maxReplicas: 5
   ```

5. Create the worker from `deploy/azure/worker.yaml`: the same, without
   ingress or probes (Container Apps has no command probes, and the
   worker serves no HTTP), with its own process types and one replica
   (`az containerapp create -n blog-worker -g blog --yaml
   deploy/azure/worker.yaml`):

   ```yaml
   identity:
     type: UserAssigned
     userAssignedIdentities:
       /subscriptions/SUBSCRIPTION/resourceGroups/blog/providers/Microsoft.ManagedIdentity/userAssignedIdentities/blog: {}
   properties:
     environmentId: /subscriptions/SUBSCRIPTION/resourceGroups/blog/providers/Microsoft.App/managedEnvironments/blog
     configuration:
       activeRevisionsMode: Single
       registries:
         - server: blogregistry.azurecr.io
           identity: /subscriptions/SUBSCRIPTION/resourceGroups/blog/providers/Microsoft.ManagedIdentity/userAssignedIdentities/blog
       secrets:
         - name: app-key
           keyVaultUrl: https://blog-vault.vault.azure.net/secrets/app-key
           identity: /subscriptions/SUBSCRIPTION/resourceGroups/blog/providers/Microsoft.ManagedIdentity/userAssignedIdentities/blog
         - name: db-password
           keyVaultUrl: https://blog-vault.vault.azure.net/secrets/db-password
           identity: /subscriptions/SUBSCRIPTION/resourceGroups/blog/providers/Microsoft.ManagedIdentity/userAssignedIdentities/blog
     template:
       terminationGracePeriodSeconds: 40
       containers:
         - name: worker
           image: blogregistry.azurecr.io/blog:1.0.0
           resources: {cpu: 0.5, memory: 1Gi}
           env:
             - {name: APP_PROCESS_TYPES, value: "worker,scheduler"}
             - {name: APP_URL, value: "https://blog.example.com"}
             - {name: DB_DRIVER, value: postgres}
             - {name: DB_HOST, value: blog-db.postgres.database.azure.com}
             - {name: DB_NAME, value: blog}
             - {name: DB_USER, value: blog}
             - {name: DB_PASSWORD, secretRef: db-password}
             - {name: APP_KEY, secretRef: app-key}
             - {name: CACHE_DRIVER, value: database}
             - {name: SESSION_DRIVER, value: database}
             - {name: QUEUE_DRIVER, value: database}
       # No ingress: without minReplicas it would scale to zero and stay there.
       scale:
         minReplicas: 1
         maxReplicas: 1
   ```

For each new version, update the job's image, run it and wait for it,
then update the apps:

```sh
IMAGE=blogregistry.azurecr.io/blog:1.0.1
az containerapp job update -n blog-migrate -g blog --image "$IMAGE" -o none
RUN=$(az containerapp job start -n blog-migrate -g blog --query name -o tsv)
while :; do
  STATUS=$(az containerapp job execution show -n blog-migrate -g blog \
    --job-execution-name "$RUN" --query properties.status -o tsv)
  case "$STATUS" in
    Succeeded) break ;;
    Failed|Stopped|Degraded) echo "migrate: $STATUS"; exit 1 ;;
  esac
  sleep 10
done
az containerapp update -n blog -g blog --image "$IMAGE"
az containerapp update -n blog-worker -g blog --image "$IMAGE"
```

A container app keeps its old revision serving until the new one is
ready, and gives a process `terminationGracePeriodSeconds` (40 here)
between SIGTERM and SIGKILL, above `APP_SHUTDOWN_TIMEOUT`'s 30 seconds.
Its disk doesn't last: uploads need `STORAGE_DRIVER=s3` (with an
S3-compatible store) or a mounted Azure Files share on `/data`.

The client's IP: the environment's proxy adds it, rightmost, to
`X-Forwarded-For`, but the address it connects from isn't documented.
After the first deploy, the request log's `ip` field shows it: put it,
or its range (the environment's infrastructure subnet, with a virtual
network of your own), in `HTTP_TRUSTED_PROXIES`.

The platform's docs: [the YAML](https://learn.microsoft.com/azure/container-apps/azure-resource-manager-api-spec),
[health probes](https://learn.microsoft.com/azure/container-apps/health-probes),
[scaling](https://learn.microsoft.com/azure/container-apps/scale-app),
[jobs](https://learn.microsoft.com/azure/container-apps/jobs),
[secrets and Key Vault](https://learn.microsoft.com/azure/container-apps/manage-secrets),
[pulling with a managed identity](https://learn.microsoft.com/azure/container-apps/managed-identity-image-pull),
[the proxy's headers](https://learn.microsoft.com/azure/container-apps/network-proxy),
[PostgreSQL's TLS](https://learn.microsoft.com/azure/postgresql/security/security-tls).

## Amazon ECS on Fargate

The web server and the background work are two ECS services, each with
its task definition; the web one sits behind an Application Load
Balancer. The migrations run as a one-off task. The database is RDS
for PostgreSQL, whose certificates come from Amazon's own authorities,
not public ones: the image carries their bundle.

1. Add the bundle to the image. In the `Dockerfile`'s final stage,
   before `USER`:

   ```dockerfile
   ADD --chmod=0644 https://truststore.pki.rds.amazonaws.com/global/global-bundle.pem /etc/ssl/rds/global-bundle.pem
   ```

   and push the image to ECR (`111122223333.dkr.ecr.eu-west-1.amazonaws.com/blog:1.0.0`).

2. Put `APP_KEY` in Secrets Manager (`blog/app-key`). With RDS managing
   the database's password, its secret holds `username` and
   `password`. The task execution role needs
   `AmazonECSTaskExecutionRolePolicy` and `secretsmanager:GetSecretValue`
   on both secrets, and the log group `/ecs/blog` must exist.

3. Register the task definitions. The web one, `deploy/ecs/taskdef-web.json`:

   ```json
   {
     "family": "blog-web",
     "networkMode": "awsvpc",
     "requiresCompatibilities": [
       "FARGATE"
     ],
     "cpu": "512",
     "memory": "1024",
     "runtimePlatform": {
       "operatingSystemFamily": "LINUX",
       "cpuArchitecture": "X86_64"
     },
     "executionRoleArn": "arn:aws:iam::111122223333:role/blog-ecs-execution",
     "containerDefinitions": [
       {
         "name": "app",
         "image": "111122223333.dkr.ecr.eu-west-1.amazonaws.com/blog:1.0.0",
         "essential": true,
         "portMappings": [
           {
             "containerPort": 8080,
             "protocol": "tcp"
           }
         ],
         "environment": [
           {
             "name": "APP_PROCESS_TYPES",
             "value": "web"
           },
           {
             "name": "APP_URL",
             "value": "https://blog.example.com"
           },
           {
             "name": "DB_DRIVER",
             "value": "postgres"
           },
           {
             "name": "DB_HOST",
             "value": "blog.abcdefghijkl.eu-west-1.rds.amazonaws.com"
           },
           {
             "name": "DB_NAME",
             "value": "blog"
           },
           {
             "name": "DB_TLS_CA",
             "value": "/etc/ssl/rds/global-bundle.pem"
           },
           {
             "name": "CACHE_DRIVER",
             "value": "database"
           },
           {
             "name": "SESSION_DRIVER",
             "value": "database"
           },
           {
             "name": "QUEUE_DRIVER",
             "value": "database"
           },
           {
             "name": "HTTP_TRUSTED_PROXIES",
             "value": "10.0.0.0/24,10.0.1.0/24"
           }
         ],
         "secrets": [
           {
             "name": "APP_KEY",
             "valueFrom": "arn:aws:secretsmanager:eu-west-1:111122223333:secret:blog/app-key-AbCdEf"
           },
           {
             "name": "DB_USER",
             "valueFrom": "arn:aws:secretsmanager:eu-west-1:111122223333:secret:rds!db-1234-AbCdEf:username::"
           },
           {
             "name": "DB_PASSWORD",
             "valueFrom": "arn:aws:secretsmanager:eu-west-1:111122223333:secret:rds!db-1234-AbCdEf:password::"
           }
         ],
         "healthCheck": {
           "command": [
             "CMD",
             "/app/blog",
             "health:check",
             "--live"
           ],
           "interval": 15,
           "timeout": 5,
           "retries": 3,
           "startPeriod": 30
         },
         "stopTimeout": 45,
         "logConfiguration": {
           "logDriver": "awslogs",
           "options": {
             "awslogs-group": "/ecs/blog",
             "awslogs-region": "eu-west-1",
             "awslogs-stream-prefix": "web"
           }
         }
       }
     ]
   }
   ```

   The worker's, `deploy/ecs/taskdef-worker.json`, is the same with
   `"family": "blog-worker"`, `APP_PROCESS_TYPES` set to
   `worker,scheduler`, no port mapping, no health check and no
   `HTTP_TRUSTED_PROXIES`.

   ```sh
   aws ecs register-task-definition --cli-input-json file://deploy/ecs/taskdef-web.json
   aws ecs register-task-definition --cli-input-json file://deploy/ecs/taskdef-worker.json
   ```

4. Create the load balancer's target group: `ip` targets on port 8080,
   health check path `/health/ready`, and a short deregistration delay
   (`deregistration_delay.timeout_seconds` 30) instead of the 300
   seconds by default.

5. Run the migrations, `deploy/ecs/run-migrate.json`, a task of the
   worker's definition whose command is `migrate`:

   ```json
   {
     "cluster": "blog",
     "taskDefinition": "blog-worker",
     "launchType": "FARGATE",
     "networkConfiguration": {
       "awsvpcConfiguration": {
         "subnets": [
           "subnet-app-a",
           "subnet-app-b"
         ],
         "securityGroups": [
           "sg-blog"
         ],
         "assignPublicIp": "DISABLED"
       }
     },
     "overrides": {
       "containerOverrides": [
         {
           "name": "app",
           "command": [
             "migrate"
           ]
         }
       ]
     }
   }
   ```

   ```sh
   TASK=$(aws ecs run-task --cli-input-json file://deploy/ecs/run-migrate.json \
     --query 'tasks[0].taskArn' --output text)
   aws ecs wait tasks-stopped --cluster blog --tasks "$TASK"
   CODE=$(aws ecs describe-tasks --cluster blog --tasks "$TASK" \
     --query 'tasks[0].containers[0].exitCode' --output text)
   [ "$CODE" = 0 ] || { echo "migrate failed: $CODE"; exit 1; }
   ```

6. Create the services, `deploy/ecs/service-web.json`:

   ```json
   {
     "cluster": "blog",
     "serviceName": "blog-web",
     "taskDefinition": "blog-web",
     "desiredCount": 2,
     "launchType": "FARGATE",
     "networkConfiguration": {
       "awsvpcConfiguration": {
         "subnets": [
           "subnet-app-a",
           "subnet-app-b"
         ],
         "securityGroups": [
           "sg-blog"
         ],
         "assignPublicIp": "DISABLED"
       }
     },
     "loadBalancers": [
       {
         "targetGroupArn": "arn:aws:elasticloadbalancing:eu-west-1:111122223333:targetgroup/blog-web/0123456789abcdef",
         "containerName": "app",
         "containerPort": 8080
       }
     ],
     "healthCheckGracePeriodSeconds": 30,
     "deploymentConfiguration": {
       "minimumHealthyPercent": 100,
       "maximumPercent": 200,
       "deploymentCircuitBreaker": {
         "enable": true,
         "rollback": true
       }
     }
   }
   ```

   and `deploy/ecs/service-worker.json`, the same without the load
   balancer and `healthCheckGracePeriodSeconds` (which needs one), with
   `"serviceName": "blog-worker"`, `"taskDefinition": "blog-worker"`,
   one task and `"minimumHealthyPercent": 0,
   "maximumPercent": 100`, so the old task stops before the new one
   starts:

   ```sh
   aws ecs create-service --cli-input-json file://deploy/ecs/service-web.json
   aws ecs create-service --cli-input-json file://deploy/ecs/service-worker.json
   ```

For each new version, register the task definitions with the new image,
run step 5, then `aws ecs update-service --cluster blog --service
blog-web --task-definition blog-web` (and the same for `blog-worker`).
The circuit breaker rolls a deploy back when its tasks don't become
healthy.

ECS takes a task out of the load balancer before it sends SIGTERM, and
waits `stopTimeout` (45 seconds here, 120 at most on Fargate) before
SIGKILL. The container's health check runs `blog health:check --live`,
which the image has (its base has no shell): whether the process is
alive, since ECS replaces an unhealthy task, and a database that's down
for a minute shouldn't replace them all; the load balancer checks
`/health/ready` for its traffic. A task's disk doesn't last:
uploads need `STORAGE_DRIVER=s3`.

The client's IP: the load balancer adds it to `X-Forwarded-For` and
connects from addresses in its subnets: `HTTP_TRUSTED_PROXIES` lists
those subnets' ranges (here `10.0.0.0/24` and `10.0.1.0/24`).

The platform's docs: [task definition parameters](https://docs.aws.amazon.com/AmazonECS/latest/developerguide/task_definition_parameters.html),
[secrets from Secrets Manager](https://docs.aws.amazon.com/AmazonECS/latest/developerguide/secrets-envvar-secrets-manager.html),
[service parameters](https://docs.aws.amazon.com/AmazonECS/latest/developerguide/service_definition_parameters.html),
[the circuit breaker](https://docs.aws.amazon.com/AmazonECS/latest/developerguide/deployment-circuit-breaker.html),
[run-task](https://docs.aws.amazon.com/cli/latest/reference/ecs/run-task.html),
[RDS and TLS](https://docs.aws.amazon.com/AmazonRDS/latest/UserGuide/UsingWithRDS.SSL.html),
[the load balancer's headers](https://docs.aws.amazon.com/elasticloadbalancing/latest/application/x-forwarded-headers.html).

## DigitalOcean App Platform

The web server is a service and the background work a worker, both
built from the repository's `Dockerfile`; the database is a managed
PostgreSQL cluster attached to the app. A pre-deploy job would need a
run command, which replaces the image's entrypoint and isn't documented
to work without a shell, which the image doesn't have; so the
instances migrate when they start
(`DB_MIGRATE_ON_START=true`), before they run: the migrations take a
lock, so of several instances starting at once, one runs them and the
others wait. A failed migration fails the
deploy, and the old version keeps serving.

The database's certificate comes from the cluster's own authority,
which App Platform gives as text in `${db.CA_CERT}`: `DB_TLS_CA`
takes it as it is, so the connection is verified.

1. Create the database cluster (`blog-db`), and the app from
   `.do/app.yaml`:

   ```yaml
   name: blog
   region: ams
   envs:
     - {key: APP_URL, scope: RUN_TIME, value: "${APP_URL}"}
     # Encrypted when first sent: keep the EV[…] value doctl shows after.
     - {key: APP_KEY, scope: RUN_TIME, type: SECRET, value: "base64:…"}
     - {key: DB_DRIVER, scope: RUN_TIME, value: postgres}
     - {key: DB_HOST, scope: RUN_TIME, value: "${db.HOSTNAME}"}
     - {key: DB_PORT, scope: RUN_TIME, value: "${db.PORT}"}
     - {key: DB_NAME, scope: RUN_TIME, value: "${db.DATABASE}"}
     - {key: DB_USER, scope: RUN_TIME, value: "${db.USERNAME}"}
     - {key: DB_PASSWORD, scope: RUN_TIME, value: "${db.PASSWORD}"}
     # The cluster's own CA, as text: the connection is verified.
     - {key: DB_TLS_CA, scope: RUN_TIME, value: "${db.CA_CERT}"}
     - {key: CACHE_DRIVER, scope: RUN_TIME, value: database}
     - {key: SESSION_DRIVER, scope: RUN_TIME, value: database}
     - {key: QUEUE_DRIVER, scope: RUN_TIME, value: database}
     # Each new instance migrates before it runs; a lock lets one run them.
     - {key: DB_MIGRATE_ON_START, scope: RUN_TIME, value: "true"}
     # App Platform puts the client's address in do-connecting-ip.
     - {key: HTTP_CLIENT_IP_HEADER, scope: RUN_TIME, value: do-connecting-ip}
   services:
     - name: web
       github: {repo: OWNER/blog, branch: main, deploy_on_push: true}
       dockerfile_path: Dockerfile
       http_port: 8080
       instance_count: 2
       instance_size_slug: apps-s-1vcpu-1gb
       envs:
         - {key: APP_PROCESS_TYPES, scope: RUN_TIME, value: web}
       health_check:
         http_path: /health/ready
         period_seconds: 5
         failure_threshold: 3
       liveness_health_check:
         http_path: /health/live
         initial_delay_seconds: 10
       termination:
         drain_seconds: 15
         grace_period_seconds: 45
   workers:
     - name: worker
       github: {repo: OWNER/blog, branch: main, deploy_on_push: true}
       dockerfile_path: Dockerfile
       instance_count: 1
       instance_size_slug: apps-s-1vcpu-1gb
       envs:
         - {key: APP_PROCESS_TYPES, scope: RUN_TIME, value: "worker,scheduler"}
       termination:
         grace_period_seconds: 45
   databases:
     - {name: db, engine: PG, production: true, cluster_name: blog-db, db_name: blog, db_user: blog}
   ```

   ```sh
   doctl apps create --spec .do/app.yaml
   ```

2. Each push to `main` builds and deploys the app (`deploy_on_push`).
   To deploy an image from DigitalOcean's registry instead, replace
   `github` and `dockerfile_path` with `image: {registry_type: DOCR,
   repository: blog, tag: "1.0.0"}`.

A migration that takes more than about three minutes: raise
`liveness_health_check`'s `initial_delay_seconds`, since the server
listens only once the migrations have run.

App Platform waits `drain_seconds` after taking an instance out of the
load balancer, then sends SIGTERM, and kills it `grace_period_seconds`
later (45 here; 120 by default). Its disk doesn't last: uploads need
`STORAGE_DRIVER=s3` (DigitalOcean Spaces is S3-compatible).

The client's IP: App Platform's `X-Forwarded-For` holds its own
ingress's address; it puts the client's in `do-connecting-ip`, which
`HTTP_CLIENT_IP_HEADER` reads.

The platform's docs: [the app spec](https://docs.digitalocean.com/products/app-platform/reference/app-spec/),
[environment variables and bindable values](https://docs.digitalocean.com/products/app-platform/how-to/use-environment-variables/),
[health checks](https://docs.digitalocean.com/products/app-platform/how-to/manage-health-checks/),
[databases](https://docs.digitalocean.com/products/app-platform/how-to/manage-databases/),
[the client's IP](https://docs.digitalocean.com/support/where-can-i-find-the-client-ip-address-of-a-request-connecting-to-my-app/),
[limits](https://docs.digitalocean.com/products/app-platform/details/limits/).

## Kubernetes

Plain manifests that work on any cluster: GKE, EKS, AKS, DigitalOcean
Kubernetes, or your own. The web server and the background work are
two Deployments, the migrations a Job run before each rollout, and
traffic comes in through the Gateway API, which every one of these
clouds runs (the Ingress API is frozen, and Ingress NGINX retired in
March 2026). The database is your cloud's managed PostgreSQL.

The files go in `deploy/k8s/`, with a `kustomization.yaml` that sets the
image of every manifest:

```yaml
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
namespace: blog
resources:
- configmap.yaml
- migrate-job.yaml
- web-deployment.yaml
- service.yaml
- pdb.yaml
- worker-deployment.yaml
- gateway.yaml
- httproute.yaml
images:
- name: blog
  newName: registry.example.com/blog
  newTag: "1.0.0"
```

1. The settings: non-secret ones in a ConfigMap,
   `deploy/k8s/configmap.yaml`:

   ```yaml
   apiVersion: v1
   kind: ConfigMap
   metadata:
     name: blog-config
     labels:
       app.kubernetes.io/name: blog
       app.kubernetes.io/component: config
   data:
     APP_URL: "https://blog.example.com"
     DB_DRIVER: "postgres"
     DB_HOST: "db.example.internal"
     DB_NAME: "blog"
     DB_USER: "blog"
     CACHE_DRIVER: "database"
     SESSION_DRIVER: "database"
     QUEUE_DRIVER: "database"
     APP_SHUTDOWN_TIMEOUT: "30s"
     # Peers whose X-Forwarded-For is trusted. Private ranges cover the
     # in-cluster gateway pods (AKS, DOKS) and ALB nodes in the VPC (EKS).
     # On GKE also add 35.191.0.0/16,130.211.0.0/22 and the Gateway's IP.
     HTTP_TRUSTED_PROXIES: "10.0.0.0/8,172.16.0.0/12,192.168.0.0/16"
   ```

   and the secrets in a Secret made once, outside the repository:

   ```sh
   kubectl create namespace blog
   kubectl -n blog create secret generic blog-secrets \
     --from-literal=APP_KEY='base64:…' --from-literal=DB_PASSWORD='…'
   ```

   TLS to the database is verified against the system's authorities;
   for a database with its own (RDS), add its certificates to the
   Secret as PEM text, its region's bundle (a few kilobytes):
   `--from-file=DB_TLS_CA=eu-west-1-bundle.pem`.

   With your cloud's secret store, the External Secrets Operator can
   keep this Secret up to date from it. A change to the ConfigMap or the
   Secret reaches the pods when they restart:
   `kubectl -n blog rollout restart deployment`.

2. The migrations, `deploy/k8s/migrate-job.yaml`:

   ```yaml
   # Recreated on every release (Job pod templates are immutable).
   apiVersion: batch/v1
   kind: Job
   metadata:
     name: blog-migrate
     labels:
       app.kubernetes.io/name: blog
       app.kubernetes.io/component: migrate
   spec:
     backoffLimit: 2
     activeDeadlineSeconds: 600
     ttlSecondsAfterFinished: 86400
     template:
       metadata:
         labels:
           app.kubernetes.io/name: blog
           app.kubernetes.io/component: migrate
       spec:
         restartPolicy: Never
         automountServiceAccountToken: false
         securityContext:
           runAsNonRoot: true
           runAsUser: 65532
           runAsGroup: 65532
           seccompProfile:
             type: RuntimeDefault
         containers:
           - name: migrate
             image: blog
             args: ["migrate"]
             envFrom:
               - configMapRef:
                   name: blog-config
               - secretRef:
                   name: blog-secrets
             resources:
               requests:
                 cpu: 100m
                 memory: 128Mi
               limits:
                 memory: 256Mi
             securityContext:
               allowPrivilegeEscalation: false
               readOnlyRootFilesystem: true
               capabilities:
                 drop: ["ALL"]
             volumeMounts:
               - name: data
                 mountPath: /data
         volumes:
           - name: data
             emptyDir: {}
   ```

3. The web server, `deploy/k8s/web-deployment.yaml`. It runs without
   privileges on a read-only file system, with two writable folders:
   `/tmp`, where large uploads are buffered, and `/data`, where the
   image keeps local files (the app opens its default disk there when
   it starts, even with uploads elsewhere). On shutdown it keeps serving for 10
   seconds while the load balancer stops sending requests, then has
   `APP_SHUTDOWN_TIMEOUT` to finish:

   ```yaml
   apiVersion: apps/v1
   kind: Deployment
   metadata:
     name: blog-web
     labels:
       app.kubernetes.io/name: blog
       app.kubernetes.io/component: web
   spec:
     replicas: 2
     revisionHistoryLimit: 5
     selector:
       matchLabels:
         app.kubernetes.io/name: blog
         app.kubernetes.io/component: web
     strategy:
       type: RollingUpdate
       rollingUpdate:
         maxSurge: 1
         maxUnavailable: 0
     template:
       metadata:
         labels:
           app.kubernetes.io/name: blog
           app.kubernetes.io/component: web
       spec:
         automountServiceAccountToken: false
         # preStop sleep (10s) + APP_SHUTDOWN_TIMEOUT (30s) + 5s margin
         terminationGracePeriodSeconds: 45
         securityContext:
           runAsNonRoot: true
           runAsUser: 65532
           runAsGroup: 65532
           seccompProfile:
             type: RuntimeDefault
         containers:
           - name: web
             image: blog
             env:
               - name: APP_PROCESS_TYPES
                 value: web
             ports:
               - name: http
                 containerPort: 8080
             envFrom:
               - configMapRef:
                   name: blog-config
               - secretRef:
                   name: blog-secrets
             resources:
               requests:
                 cpu: 100m
                 memory: 128Mi
               limits:
                 memory: 256Mi
             securityContext:
               allowPrivilegeEscalation: false
               readOnlyRootFilesystem: true
               capabilities:
                 drop: ["ALL"]
             startupProbe:
               httpGet:
                 path: /health/live
                 port: http
               periodSeconds: 2
               failureThreshold: 30
             readinessProbe:
               httpGet:
                 path: /health/ready
                 port: http
               periodSeconds: 5
               failureThreshold: 2
             livenessProbe:
               httpGet:
                 path: /health/live
                 port: http
               periodSeconds: 10
               timeoutSeconds: 2
               failureThreshold: 3
             lifecycle:
               preStop:
                 sleep:
                   seconds: 10
             volumeMounts:
               - name: tmp
                 mountPath: /tmp
               - name: data
                 mountPath: /data
         volumes:
           - name: tmp
             emptyDir:
               sizeLimit: 256Mi
           - name: data
             emptyDir: {}
   ```

   Its Service and disruption budget, `deploy/k8s/service.yaml` and
   `deploy/k8s/pdb.yaml`:

   ```yaml
   apiVersion: v1
   kind: Service
   metadata:
     name: blog-web
     labels:
       app.kubernetes.io/name: blog
       app.kubernetes.io/component: web
   spec:
     type: ClusterIP
     selector:
       app.kubernetes.io/name: blog
       app.kubernetes.io/component: web
     ports:
       - name: http
         port: 80
         targetPort: http
         appProtocol: http
   ---
   apiVersion: policy/v1
   kind: PodDisruptionBudget
   metadata:
     name: blog-web
   spec:
     maxUnavailable: 1
     unhealthyPodEvictionPolicy: AlwaysAllow
     selector:
       matchLabels:
         app.kubernetes.io/name: blog
         app.kubernetes.io/component: web
   ```

4. The background work, `deploy/k8s/worker-deployment.yaml`: the web
   Deployment's pod without ports or probes (the worker serves no
   HTTP), its own `APP_PROCESS_TYPES`, one replica and the `Recreate`
   strategy, so the old pod stops before the new one starts:

   ```yaml
   # Queue workers and the scheduler. One replica and Recreate, so the
   # scheduler doesn't run twice during a rollout. To scale the workers,
   # split this into a Deployment with APP_PROCESS_TYPES=worker (several
   # replicas, RollingUpdate) and one with APP_PROCESS_TYPES=scheduler.
   apiVersion: apps/v1
   kind: Deployment
   metadata:
     name: blog-worker
     labels:
       app.kubernetes.io/name: blog
       app.kubernetes.io/component: worker
   spec:
     replicas: 1
     revisionHistoryLimit: 5
     selector:
       matchLabels:
         app.kubernetes.io/name: blog
         app.kubernetes.io/component: worker
     strategy:
       type: Recreate
     template:
       metadata:
         labels:
           app.kubernetes.io/name: blog
           app.kubernetes.io/component: worker
       spec:
         automountServiceAccountToken: false
         # APP_SHUTDOWN_TIMEOUT (30s) + margin; raise both if jobs run longer
         terminationGracePeriodSeconds: 40
         securityContext:
           runAsNonRoot: true
           runAsUser: 65532
           runAsGroup: 65532
           seccompProfile:
             type: RuntimeDefault
         containers:
           - name: worker
             image: blog
             env:
               - name: APP_PROCESS_TYPES
                 value: "worker,scheduler"
             envFrom:
               - configMapRef:
                   name: blog-config
               - secretRef:
                   name: blog-secrets
             resources:
               requests:
                 cpu: 100m
                 memory: 128Mi
               limits:
                 memory: 256Mi
             securityContext:
               allowPrivilegeEscalation: false
               readOnlyRootFilesystem: true
               capabilities:
                 drop: ["ALL"]
             volumeMounts:
               - name: tmp
                 mountPath: /tmp
               - name: data
                 mountPath: /data
         volumes:
           - name: tmp
             emptyDir:
               sizeLimit: 256Mi
           - name: data
             emptyDir: {}
   ```

5. The way in, `deploy/k8s/gateway.yaml` and `deploy/k8s/httproute.yaml`:
   a Gateway with HTTPS (its certificate from cert-manager here), a
   route to the Service, and a redirect from HTTP:

   ```yaml
   # The per-cloud piece. gatewayClassName:
   #   GKE: gke-l7-global-external-managed   AKS: approuting-istio
   #   DOKS: cilium                          EKS: see the EKS notes (no certificateRefs)
   apiVersion: gateway.networking.k8s.io/v1
   kind: Gateway
   metadata:
     name: blog
     annotations:
       cert-manager.io/cluster-issuer: letsencrypt  # drop if the cert comes from elsewhere
   spec:
     gatewayClassName: CHANGE-ME
     listeners:
       - name: http
         protocol: HTTP
         port: 80
         hostname: blog.example.com
         allowedRoutes:
           namespaces:
             from: Same
       - name: https
         protocol: HTTPS
         port: 443
         hostname: blog.example.com
         tls:
           mode: Terminate
           certificateRefs:
             - name: blog-tls
         allowedRoutes:
           namespaces:
             from: Same
   ---
   apiVersion: gateway.networking.k8s.io/v1
   kind: HTTPRoute
   metadata:
     name: blog
   spec:
     parentRefs:
       - name: blog
         sectionName: https
     hostnames: ["blog.example.com"]
     rules:
       - backendRefs:
           - name: blog-web
             port: 80
   ---
   apiVersion: gateway.networking.k8s.io/v1
   kind: HTTPRoute
   metadata:
     name: blog-https-redirect
   spec:
     parentRefs:
       - name: blog
         sectionName: http
     hostnames: ["blog.example.com"]
     rules:
       - filters:
           - type: RequestRedirect
             requestRedirect:
               scheme: https
               statusCode: 301
   ```

   The Gateway is the part that differs between clouds:

   | Cloud | `gatewayClassName` | Also |
   |---|---|---|
   | GKE | `gke-l7-global-external-managed` | Turn the Gateway API on (Standard clusters); a `HealthCheckPolicy` for the Service with `requestPath: /health/ready` and port 8080, since the load balancer checks `/` otherwise |
   | EKS | One you create for the AWS Load Balancer Controller (`controllerName: gateway.k8s.aws/alb`) | The certificate from ACM rather than `certificateRefs`; a `TargetGroupConfiguration` with `targetType: ip` and the health check path `/health/ready` |
   | AKS | `approuting-istio` | Turn on the application routing add-on's Gateway API |
   | DigitalOcean | `cilium` | Clusters with VPC-native networking, on Kubernetes 1.33 or later |

Each release:

```sh
cd deploy/k8s && kustomize edit set image blog=registry.example.com/blog:1.0.1 && cd -
kubectl -n blog delete job blog-migrate --ignore-not-found
kubectl apply -k deploy/k8s -l 'app.kubernetes.io/component in (config,migrate)'
while :; do  # until the Job has completed or failed
  STATE=$(kubectl -n blog get job blog-migrate -o jsonpath='{.status.conditions[?(@.status=="True")].type}')
  case "$STATE" in
    *Complete*) break ;;
    *Failed*) kubectl -n blog logs job/blog-migrate; exit 1 ;;
  esac
  sleep 5
done
kubectl apply -k deploy/k8s
kubectl -n blog rollout status deployment/blog-web
kubectl -n blog rollout status deployment/blog-worker
```

A Job's pod can't be changed, so it is made again each release; the
second `apply` starts the rollout, which replaces web pods one at a
time, each taking traffic once `/health/ready` answers. A pod's `/data`
is emptied when the pod goes, so uploads need `STORAGE_DRIVER=s3` or
`gcs`, or a persistent volume on `/data`. The `lifecycle.preStop.sleep` action needs
Kubernetes 1.30 or later.

The client's IP: `HTTP_TRUSTED_PROXIES` in the ConfigMap trusts the
private ranges, where the gateway's proxies and the load balancer's
nodes are. They hold the cluster's pods too: any pod, or any machine
in the network, that reaches `blog-web` directly can choose the IP the
app sees ([the warning in Deploy](deployment.md#5-put-https-in-front)).
Narrow the list to your gateway's and load balancer's ranges, or keep
other pods away from `blog-web` with a NetworkPolicy. On GKE, Google's
load balancer connects from `35.191.0.0/16` and `130.211.0.0/22` and
adds its own address to `X-Forwarded-For` after the client's: add those
ranges and the Gateway's address.

The docs: [Deployments](https://kubernetes.io/docs/concepts/workloads/controllers/deployment/),
[Jobs](https://kubernetes.io/docs/concepts/workloads/controllers/job/),
[container lifecycle hooks](https://kubernetes.io/docs/concepts/containers/container-lifecycle-hooks/),
[the Gateway API](https://gateway-api.sigs.k8s.io/),
[GKE's Gateway](https://docs.cloud.google.com/kubernetes-engine/docs/how-to/deploying-gateways),
[EKS: the AWS Load Balancer Controller's Gateway](https://kubernetes-sigs.github.io/aws-load-balancer-controller/latest/guide/gateway/gateway/),
[AKS's Gateway API](https://learn.microsoft.com/azure/aks/app-routing-gateway-api),
[DigitalOcean Kubernetes' Gateway API](https://docs.digitalocean.com/products/kubernetes/how-to/use-gateway-api/),
[cert-manager with a Gateway](https://cert-manager.io/docs/usage/gateway/).

## Fly.io

`fly launch` finds the `Dockerfile`; edit the `fly.toml` it writes:

```toml
app = "blog"
primary_region = "ams"
# Fly sends SIGINT and waits 5 seconds by default.
kill_signal = "SIGTERM"
kill_timeout = 35

# Runs in a temporary machine before each deploy (no volumes there).
[deploy]
  release_command = "migrate"

# Each value replaces the image's CMD; the binary stays the entrypoint.
[processes]
  web = "run --only=web"
  worker = "run --only=worker,scheduler"

[env]
  APP_URL = "https://blog.fly.dev"
  # Fly's proxy puts the client's address in Fly-Client-IP.
  HTTP_CLIENT_IP_HEADER = "Fly-Client-IP"
  CACHE_DRIVER = "database"
  SESSION_DRIVER = "database"
  QUEUE_DRIVER = "database"

[http_service]
  internal_port = 8080
  force_https = true
  processes = ["web"]

  [[http_service.checks]]
    grace_period = "15s"
    interval = "30s"
    method = "GET"
    timeout = "5s"
    path = "/health/ready"
```

`Fly-Client-IP` is the address Fly's proxy accepted the connection
from. The machines of your Fly organization can also reach the app on
its private network, without the proxy, and so choose the IP it sees:
keep machines you don't trust out of the organization.

Set the secrets with `fly secrets set APP_KEY=base64:… DB_URL=…`, then
`fly deploy`. The release command has no volume, so this suits
PostgreSQL or MySQL. For SQLite, run one machine of a single process
(no `[processes]`), with a volume (`[[mounts]] source = "blog_data"`,
`destination = "/data"`) and without `release_command`: the image
migrates when it starts (`DB_MIGRATE_ON_START=true`, set by the
`Dockerfile`), and the health check passes once it has.

The platform's docs: [fly.toml](https://docs.fly.io/reference/configuration/),
[process groups](https://docs.fly.io/launch/processes/),
[request headers](https://docs.fly.io/networking/request-headers/).

## Render

A `render.yaml` blueprint with a PostgreSQL database; the
web service and the worker share their settings through an environment
group (except `APP_KEY` and `DB_URL`, which a group can't hold):

```yaml
services:
  - type: web
    name: blog
    runtime: docker
    plan: starter
    healthCheckPath: /health/ready
    # Render's commands replace the image's entrypoint: give the binary's path.
    preDeployCommand: /app/blog migrate
    dockerCommand: /app/blog run --only=web
    envVars:
      - fromGroup: blog
      - key: APP_KEY
        sync: false            # asked for on the first deploy: the same key for both
      - key: DB_URL
        fromDatabase:
          name: blog-db
          property: connectionString
  - type: worker
    name: blog-worker
    runtime: docker
    plan: starter
    dockerCommand: /app/blog run --only=worker,scheduler
    envVars:
      - fromGroup: blog
      - key: APP_KEY
        sync: false            # asked for on the first deploy: the same key for both
      - key: DB_URL
        fromDatabase:
          name: blog-db
          property: connectionString

envVarGroups:
  - name: blog
    envVars:
      - key: APP_URL
        value: https://blog.onrender.com
      - key: DB_DRIVER
        value: postgres
      - key: CACHE_DRIVER
        value: database
      - key: SESSION_DRIVER
        value: database
      - key: QUEUE_DRIVER
        value: database

databases:
  - name: blog-db
```

The client's IP: Render doesn't document the address its proxy
connects from. After the first deploy, the request log's `ip` field
shows it: put it, or its range, in `HTTP_TRUSTED_PROXIES`, and check
that the log then shows your own address when you visit.

`APP_KEY` must be `base64:` and 32 bytes, so don't let Render generate
it (`generateValue`). The pre-deploy command needs a paid instance
type.

The platform's docs: [blueprints](https://render.com/docs/blueprint-spec),
[deploys and the pre-deploy command](https://render.com/docs/deploys),
[health checks](https://render.com/docs/health-checks).

## Common problems

| Symptom | Cause | Fix |
|---|---|---|
| Every request logs the same IP, and rate limits hit everyone | The platform's proxy isn't trusted, so its address is taken for the client's | Put the address the request log's `ip` field shows in `HTTP_TRUSTED_PROXIES`, or set `HTTP_CLIENT_IP_HEADER` where the platform has such a header |
| New instances never become ready | Migrations are pending (`/health/ready` answers 503, the log says which) | Run the migration step before the new version, and check that it succeeded |
| `unknown process type "…"` at start | `APP_PROCESS_TYPES` names a type the app doesn't have | Use the app's types; the error lists them |
| The worker or the scheduler runs nowhere | Every process got `APP_PROCESS_TYPES=web` | Give the background process `worker,scheduler` |
| `x509: certificate signed by unknown authority` connecting to the database | The database's certificate comes from its own authority | Give `DB_TLS_CA` its certificates, as a file in the image or as PEM text |
| Uploaded files disappear after a deploy | The platform's disk doesn't outlive the instance | `STORAGE_DRIVER=s3` or `gcs` |
| Jobs cut off at each deploy | The platform kills the process before it finishes | Raise its grace period, or lower `APP_SHUTDOWN_TIMEOUT` below it |

## Next steps

- [Deploy](deployment.md): the image, the settings, migrations and
  process types.
- [Secure your app](security.md): the checklist before going live.
- [Store files](storage.md) in S3 or Google Cloud Storage.
- [The settings reference](../reference/configuration.md).

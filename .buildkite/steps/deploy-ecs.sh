#!/bin/bash
set -euo pipefail

export AWS_DEFAULT_REGION=us-east-1

cluster="buildkite-ecs"
task_family="statusbot"
service_name="statusbot"
image="032379705303.dkr.ecr.us-east-1.amazonaws.com/statusbot:${BUILDKITE_BUILD_NUMBER}"

## This is the template definition of your containers
task_definition=$(cat << JSON
[{
  "image": "${image}",
  "logConfiguration": {
      "logDriver": "awslogs",
      "options": {
          "awslogs-group": "/ecs/statusbot",
          "awslogs-region": "us-east-1",
          "awslogs-stream-prefix": "${service_name}"
      }
  },
  "memoryReservation": 64,
  "name": "statusbot"
}]
JSON
)

echo "--- :ecs: Registering new task definition for ${task_family}"
task_revision=$(
  aws ecs register-task-definition \
    --family "${task_family}" \
    --container-definitions "$task_definition" \
    | jq '.taskDefinition.revision')
echo "Registered ${task_family}:${task_revision}"

echo "--- :ecs: Updating service for ${service_name}"
aws ecs update-service \
  --cluster "${cluster}" \
  --service "${service_name}" \
  --task-definition "${task_family}:${task_revision}"

## Now we wait till it's stable
echo "--- :ecs: Waiting for services to stabilize"
aws ecs wait services-stable \
  --cluster "${cluster}" \
  --services "${service_name}"

aws ecs describe-services \
  --cluster "${cluster}" \
  --service "${service_name}"

echo "+++ :ecs: 🚀"

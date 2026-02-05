# The Great Stories — System Architecture Diagram

This diagram shows the main components and data flow. See [architecture.md](./architecture.md) for detailed design.

**Rich view:** Open [architecture-diagram.html](./architecture-diagram.html) in a browser for an HTML block diagram (User left, Gemini and webhooks right).

```mermaid
graph LR
    subgraph Left[External]
        User[User/Client]
    end

    subgraph System[The Great Stories System]
        subgraph APILayer[API Layer]
            API[API Service]
        end

        subgraph ProcLayer[Processing Layer]
            Worker[Worker Service]
            Dispatcher[Webhook Dispatcher]
        end

        subgraph DataLayer[Data Layer]
            Postgres[(PostgreSQL)]
            S3[S3 Storage]
        end

        subgraph Queues[Message Queues]
            KafkaJobs[Jobs]
            KafkaEvents[Events / Webhooks]
        end
    end

    subgraph Right[External]
        subgraph GeminiAPI[Gemini API]
            Gemini[Google AI]
        end
        Webhook[User Webhook]
    end

    User --> API
    User -.-> API
    API <--> Postgres
    API --> KafkaJobs
    KafkaJobs --> Worker
    Worker <--> Gemini
    Worker --> Postgres
    Worker --> S3
    Worker --> KafkaEvents
    KafkaEvents --> Dispatcher
    Dispatcher --> Postgres
    Dispatcher --> Webhook
    API -.-> S3

    classDef service fill:#4A90E2,stroke:#2E5C8A,stroke-width:2px,color:#fff
    classDef storage fill:#50C878,stroke:#2E7D4E,stroke-width:2px,color:#fff
    classDef queue fill:#F5A623,stroke:#C77F1B,stroke-width:2px,color:#fff
    classDef external fill:#E94B3C,stroke:#A73529,stroke-width:2px,color:#fff

    class API,Worker,Dispatcher service
    class Postgres,S3 storage
    class KafkaJobs,KafkaEvents queue
    class User,Gemini,Webhook external
```

## Flow Summary

| Phase | Steps | Description |
|-------|-------|-------------|
| **Job creation** | 1–5 | User submits text → API validates & checks quota → Creates job & segments in DB → Publishes to Kafka → Returns job ID |
| **Processing** | 6–12 | Worker consumes job → Calls Gemini for segmentation, narration, images, audio → Stores assets in S3 and metadata in Postgres → Publishes completion event |
| **Webhook** | 13–16 | Dispatcher consumes event → Fetches job details → POSTs to user webhook with retries → Records delivery status |
| **Polling** | Dotted | User can poll job status and download assets via signed URLs |

# Shop architecture

## Shop containers

```mermaid
sequenceDiagram
    web->>orders: place order
```

```mermaid
flowchart LR
    customer([Customer]) --> web[Web App]
    web -->|HTTPS| orders[Orders API]
    orders -->|HTTPS| billing[Billing]
    orders -->|TCP| db[(Orders DB)]
```

```bash
curl -X POST http://localhost:8085/chat \
  -H "Content-Type: application/json" \
  -d '{
    "prompts": [
      {
        "userInput": "Add a joke at the end",
        "vendor": "openai",
        "model": "gpt-4",
        "contextName": "general_context.md",
        "patternName": "general",
        "strategyName": "",
        "obsidianFile": "test.md"
      }
    ],
    "language": "en",
    "temperature": 0.7,
    "topP": 1.0,
    "frequencyPenalty": 0.0,
    "presencePenalty": 0.0
  }'

```

```bash
curl http://localhost:8080/patterns/names
```

```bash
curl http://localhost:8080/obsidian/files
```

```bash
curl http://localhost:8080/vendors/names
```

```bash
curl http://localhost:8080/models/names
```

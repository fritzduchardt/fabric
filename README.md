# Run locally

```bash
docker network create fabric
docker run --network fabric -v $HOME/projects/github/ai-tools/fabric/patterns:/patterns -v $HOME/.config/fabric:/root/.config/fabric fabric
``` 

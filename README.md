# get-pod-app-language

A very simple, silly little kubectl plugin / utility that guesses which language an application running in a kubernetes pod was written in. 
Just a PoC after learning Go to prove myself I can write something that's "useful". :)

## Usage

Note that kubectl-get-pod-app-language is expected to be installed as a `kubectl` plugin - see installation instructions [here](https://kubernetes.io/docs/tasks/extend-kubectl/kubectl-plugins/#installing-kubectl-plugins).

For standalone usage, first run `go build -o app`.
Then, choose any of the available commands to run your application with:

### `./app get-pod-app-language`

Guesses the language. An interactive prompt will guide you towards available pods and containers.
The actual guessing is done by looking at each command in `docker history <image>` and matching it against a set of available
language heuristics, stored in `heuristics.json`.

### `./app get-pod-app-language list-heuristics`

Lists all possible heuristics (derived from `heuristics.json`) that might indicate the language
an application was written in. Used by `get-pod-app-language` to make educated guesses. 

### `./app get-pod-app-language add-to-heuristic <language> <command>`

An easy way to add a new heuristic - just run `get-pod-app-language add-to-heuristic java ./gradlew` to add the heuristic to `heuristic.json`.

## TODO
- [ ] Add support for remote docker registries, including secret mgmt.
- [ ] Add proper CLI support using Cobra, there's a mish-mash of argument and variable passing in the code that doesn't make a lot of sense
- [ ] Support more heuristics - `exec`ing into the container and listing files, looking at actual dockerfile if available, etc..
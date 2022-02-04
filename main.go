/*
pacakage kubectl-get-pod-app-language is a kubectl plugin that determines what language an application running inside a pod was written in.
In effect, it does not find the language in a deterministic way - it guesses it based on telling commands
(npm install, rustc, etc..) in the history of the build process for a given image.
*/

package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/client"
	"github.com/manifoldco/promptui"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"
)

var heuristicsFile string = "heuristics.json"

// TODO: Probably a bad way to define the struct, use singular instead of plural for easier reading
type heuristics struct {
	Language string   `json:"language"`
	Commands []string `json:"commands"`
}

func main() {
	// TODO: This can't be the write way to write CLI arg parsing logic, right? Find the best practice.
	if len(os.Args) < 2 {
		printUsageString("no-command")
	} else if len(os.Args) == 2 {
		if os.Args[1] == "get-pod-app-language" {
			getPodLanguage(os.Args)
		} else {
			printUsageString("wrong-command")
		}
	} else {
		if os.Args[2] == "help" || os.Args[1] == "--help" || os.Args[1] == "-h" {
			printUsageString("usage")
		} else {
			printUsageString("subcommand-issue")
		}
	}
}

func getPodLanguage(args []string) {
	// Argcheck
	if len(os.Args) > 3 && os.Args[2] == "add-to-heuristic" {
		addToHeuristic(os.Args)
	} else if len(os.Args) > 3 && os.Args[2] == "list-heuristics" {
		listHeuristics()
	} else {

		// See https://github.com/kubernetes/client-go/blob/master/examples/out-of-cluster-client-configuration/main.go
		// Get current relevant context from kubeconfig, then create
		// command line flags out of it that will be passed on to relevant k8s components
		home := homedir.HomeDir()
		kubeConfigPath := filepath.Join(home, ".kube", "config")
		kubeConfigflags := flag.String("kubeconfig", kubeConfigPath, "absolute path to the kubeconfig file")
		flag.Parse()
		config, err := clientcmd.BuildConfigFromFlags("", *kubeConfigflags)
		exitOnError(err)

		// Get the clientset - which are all the clients for all groups.
		// Each group has exactly one version included in a Clientset.
		// Basically - get all relevant clients for all relevant "things" for the specific *version*
		// that we're currently on for all groups. I think. :()
		clientset, err := kubernetes.NewForConfig(config)
		exitOnError(err)

		// Get the list of all pods in the cluster currently
		pods, err := clientset.CoreV1().Pods("").List(context.TODO(), v1.ListOptions{})
		exitOnError(err)
		var podNames []string

		// Create a mapping between all pods to their containers (and the underlying images)
		containersAndImagesByPod := make(map[string]map[string]string)
		for _, pod := range pods.Items {
			// Saves me from ranging over the keys of the mapping later
			podNames = append(podNames, pod.Name)
			for _, container := range pod.Spec.Containers {
				containerMap := map[string]string{
					container.Name: container.Image,
				}
				containersAndImagesByPod[pod.Name] = containerMap
			}
		}

		// Prompt user to choose the pod and the relevant container
		podPrompt := promptui.Select{
			Label: "Choose a pod",
			Items: podNames,
		}
		_, selectedPod, err := podPrompt.Run()
		exitOnError(err)

		var containerNames []string
		for container := range containersAndImagesByPod[selectedPod] {
			containerNames = append(containerNames, container)
		}

		containerPrompt := promptui.Select{
			Label: "Choose a container",
			Items: containerNames,
		}
		_, selectedContainer, err := containerPrompt.Run()

		// Once container was chosed, get container image from our mappy-map
		selectedImage := containersAndImagesByPod[selectedPod][selectedContainer]
		exitOnError(err)
		fmt.Printf("The %s container in pod %s is running %s\n", selectedContainer, selectedPod, selectedImage)

		// TODO: So, it's not going to be working with real docker registries, just minikube
		// and the local docker regitry for now, which is less efficient.
		// Make it find images also in remote, private registries, like in a real cluster:)

		// Use the docker client to get the history of the image
		cli, err := client.NewClientWithOpts(client.FromEnv)
		exitOnError(err)
		images, err := cli.ImageList(context.Background(), types.ImageListOptions{})
		exitOnError(err)
		imageFound := false
		for _, image := range images {
			// TODO: Choosing the first tag might be a bad heuristic, since an image could
			// have multiple tags. Run through all images?
			imageTag := image.RepoTags[0]
			if strings.Contains(imageTag, selectedImage) {
				imageFound = true
				// Guess languaeg for the current image
				languages := findLanguages(imageTag, cli)
				if len(languages) == 0 {
					fmt.Printf("Could not determine language of application.\nConsider adding more heuristic using add-to-heuristic - see get-pod-langauge help for more information.")
				} else if len(languages) == 1 {
					fmt.Printf("%s was most likely written in %s\n", selectedImage, languages[0])
				} else {
					fmt.Printf("%+v was most likely written in any of the following languages\n", strings.Join(languages, ","))
				}
			}
		}
		if !imageFound {
			fmt.Printf(`get-pod-app-language could not find the relevant image in the local docker registry.
			Please keep in mind that get-pod-app-language is alpha software, and does not currently support connecting to remote or private docker registries. 
			This means that it's very possible that the image is simply not accessible to get-pod-app-language, and not that it does not exist.`)
		}
	}
}

// findLanguages goes through the `docker history` of an image, command by command,
// and tries to match each command to a set of given heuristics for a specific language.
// If a heuristic was found, it notes that it's possible that the application was written
// in the corresponding language
func findLanguages(imageTag string, cli *client.Client) []string {
	var languages []string

	// Get heuristics
	rawHeuristics, err := os.ReadFile(heuristicsFile)
	exitOnError(err)
	var heuristics []heuristics
	json.Unmarshal([]byte(rawHeuristics), &heuristics)
	historyList, err := cli.ImageHistory(context.Background(), imageTag)
	exitOnError(err)

	// Start running throuhgh commands and match against heuristcs
	for _, historyListItem := range historyList {
		buildCommand := historyListItem.CreatedBy
		for _, languageHeuristics := range heuristics {
			language := languageHeuristics.Language
			for _, heuristic := range languageHeuristics.Commands {
				if strings.Contains(buildCommand, heuristic) {
					// TODO: no arr.contains() in go, it appears... is this best practice or using a "unique" slice (is that a thing?) / a set / a map a better idea?
					languageExists := false
					for _, existingLanguage := range languages {
						if existingLanguage == language {
							languageExists = true
							break
						}
					}
					if !languageExists {
						languages = append(languages, language)
					}
				}
			}
		}
	}
	return languages
}

// addToHeuristics accepts a langauge and a heuristic, makes sure it does not exist already,
// and if it does not persists it for later use.
func addToHeuristic(args []string) {
	// Argcheck
	if len(args) < 4 {
		printUsageString("add-to-heuristic-wrong-usage")
	} else {
		language := os.Args[3]
		command := os.Args[4]

		heuristics := readHeuristics()
		var languages []string
		// INEFFICIENT, but gets the job done. No .includes or .contains without looping through entire array?
		// Also, maybe just drop to maps?
		isKnownLanguage := false
		commandExists := false
		for i := 0; i < len(heuristics); i++ {
			languages = append(languages, heuristics[i].Language)
			if heuristics[i].Language == language {
				for _, existingCommand := range heuristics[i].Commands {
					if existingCommand == command {
						commandExists = true
					}
				}
				heuristics[i].Commands = append(heuristics[i].Commands, command)
				isKnownLanguage = true
			}
		}
		if !isKnownLanguage {
			fmt.Printf("%s is not a known language, please select one of the existing languages: %v\n", language, languages)
			os.Exit(1)
		} else if commandExists {
			fmt.Printf("%s exists as a command already, skipping.\n", command)
		} else {
			data, err := json.MarshalIndent(heuristics, "", "	")
			exitOnError(err)
			err = os.WriteFile(heuristicsFile, data, 0644)
			exitOnError(err)
			fmt.Printf("Appended %s to %s's list of command heuristics\n", command, language)
		}

	}
}

// listHeuristics lists all langauges and given heuristics in an easy to digest manner.
func listHeuristics() {
	heuristics := readHeuristics()
	for _, heuristic := range heuristics {
		fmt.Printf("%s\n\t%v\n", heuristic.Language, strings.Join(heuristic.Commands, ","))
	}
}

// readHeuristics parses the heuristics.json file and creates a usable heuristics array out of it.
func readHeuristics() []heuristics {
	rawHeuristics, err := os.ReadFile(heuristicsFile)
	exitOnError(err)
	var heuristics []heuristics
	err = json.Unmarshal([]byte(rawHeuristics), &heuristics)
	exitOnError(err)
	return heuristics
}

// exitOnError, well, exist on error.
// TODO: This can't be the right way to do error handling in go. Find best practice and implement.
// Error tracking library?
func exitOnError(err error) {
	if err != nil {
		panic(err)
	}
}

// printUsageString does usage string printing horribly.
// TODO: Just do it better.
// Do keep in mind that the applicaiton is expected to run as a kubernetes plugin,
// and the usage strings here are formatted accordingly
func printUsageString(code string) {
	switch code {
	case "usage":
		fmt.Println(`get-pod-app-language huesses which language the application running inside a pod was written in.
usage: kubectl get-pod-app-language
usage: kubectl get-pod-app-language add-to-heuristic <language> <command>
usage: kubectl get-pod-app-language list-heuristics`)
	case "no-command":
		fmt.Println(`No command supplied - please use get-pod-app-language as your command.
usage: kubectl get-pod-app-language
usage: kubectl get-pod-app-language add-to-heuristic <language> <command>
usage: kubectl get-pod-app-language list-heuristics`)
	case "wrong-command":
		fmt.Println(`Wrong command supplied - please use get-pod-app-language as your command.
usage: kubectl get-pod-app-language
usage: kubectl get-pod-app-language add-to-heuristic <language> <command>
usage: kubectl get-pod-app-language list-heuristics`)
	case "subcommand-issue":
		fmt.Println(`No subcommand or wrong subcommand supplied - please supply a pod name for get-pod-langugae or use a subcommand.
usage: kubectl get-pod-app-language
usage: kubectl get-pod-app-language add-to-heuristic <language> <command>
usage: kubectl get-pod-app-language list-heuristics`)
	case "no-pod-name":
		fmt.Println(`No pod name supplied - please supply a pod name.
usage: kubectl get-pod-app-language`)
	case "add-to-heuristic-wrong-usage":
		fmt.Println(`No heuristic or langauge provided - please supply both.
usage: kubectl get-pod-app-language add-to-heuristic <language> <command>`)
	}
	os.Exit(1)
}

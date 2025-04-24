package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/jacobbernoulli/discordgo"
	"github.com/joho/godotenv"
)

type Config struct {
	Token                string `env:"TOKEN"`
	Branch               string `env:"BRANCH"`
	DeploymentLocation   string `env:"DEPLOYMENT_LOCATION"`
	DeploymentChannel    string `env:"DEPLOYMENT_CHANNEL"`
	DeploymentRole       string `env:"DEPLOYMENT_ROLE"`
	DeploymentLogWebhook string `env:"DEPLOYMENT_LOG_WEBHOOK"`
}

type COMMANDS_DICTIONARY map[string]string

var (
	data     *Config
	Commands COMMANDS_DICTIONARY
)

func sendDiscordWebhookMessage(status, branch string) {
	success := status == "success"
	color := 0x008000
	description := "Deployment Successful!"
	if !success {
		color = 0x800000
		description = "Deployment Failed!"
	}

	payload := map[string]any{
		"embeds": []map[string]any{
			{
				"title":       "Deployment Status",
				"description": description,
				"color":       color,
				"fields": []map[string]any{
					{
						"name":   "Environment",
						"value":  "Production",
						"inline": true,
					},
					{
						"name":   "Branch",
						"value":  branch,
						"inline": true,
					},
				},
				"thumbnail": map[string]any{
					"url": "https://r2.fivemanage.com/3i2fhQIkHIaRFDy1YIvi8/images/image.png",
				},
				"footer": map[string]any{
					"text":     "Deployment Bot",
					"icon_url": "https://r2.fivemanage.com/3i2fhQIkHIaRFDy1YIvi8/images/image.png",
				},
				"timestamp": time.Now().Format(time.RFC3339),
			},
		},
	}

	body, _ := json.Marshal(payload)
	http.Post(data.DeploymentLogWebhook, "application/json", bytes.NewBuffer(body))
}

func getConfig() (*Config, error) {
	if err := godotenv.Load(".env"); err != nil {
		return nil, fmt.Errorf("godotenv.Load(): %w", err)
	}

	config := &Config{}
	val := reflect.ValueOf(config).Elem()

	for i := range val.NumField() {
		key := val.Field(i)
		str := val.Type().Field(i).Tag.Get("env")
		value, input := os.LookupEnv(str)
		if !input || strings.TrimSpace(value) == "" {
			return nil, fmt.Errorf("missing environment variable: %s", str)
		}
		key.SetString(value)
	}

	return config, nil
}

func getDictionary(Dictionary any) error {
	data, err := os.ReadFile("dictionary.json")
	if err != nil {
		return fmt.Errorf("os.ReadFile(): %w", err)
	}

	if err := json.Unmarshal(data, Dictionary); err != nil {
		return fmt.Errorf("json.Unmarshal(): %w", err)
	}

	return nil
}

func deploy(session *discordgo.Session, message *discordgo.MessageCreate) {
	if message.Author.Bot || message.ChannelID != data.DeploymentChannel || !strings.HasPrefix(message.Content, "!") {
		return
	}

	member, err := session.GuildMember(message.GuildID, message.Author.ID)
	if err != nil {
		return
	}

	if !slices.Contains(member.Roles, data.DeploymentRole) {
		session.ChannelMessageSend(message.ChannelID, "You do not have permission to deploy.")
		return
	}

	args := strings.Split(strings.TrimPrefix(message.Content, "!"), " ")
	if len(args) < 3 {
		return
	}

	command, branch, key := strings.ToLower(args[0]), strings.ToLower(args[1]), args[2]
	if command != "deploy" {
		session.ChannelMessageSend(message.ChannelID, fmt.Sprintf("Invalid command `(%s)` specified.", command))
		return
	}

	cache, option := Commands[key]
	if !option {
		session.ChannelMessageSend(message.ChannelID, fmt.Sprintf("Invalid key `(%s)` specified.", key))
		return
	}

	branches := regexp.MustCompile(`^[a-zA-Z0-9_-]+$`).MatchString(branch) && branch == data.Branch
	if !branches {
		session.ChannelMessageSend(message.ChannelID, fmt.Sprintf("Invalid branch `(%s)` specified.", branch))
		sendDiscordWebhookMessage("failed", branch)
		return
	}

	session.ChannelMessageSend(message.ChannelID, "Deploying ongoing...")

	go func() {
		location := strings.ReplaceAll(cache, "${LOCATION}", data.DeploymentLocation)
		replace := strings.ReplaceAll(location, "${BRANCH}", branch)

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()

		cmd := exec.CommandContext(ctx, "bash", "-c", replace)

		output, err := cmd.CombinedOutput()
		if err != nil {
			session.ChannelMessageSend(message.ChannelID, fmt.Sprintf("Deployment Failed: %s", err.Error()))
			sendDiscordWebhookMessage("failed", branch)
			log.Fatalf("cmd.CombinedOutput(): %v\n%s", err, string(output))
			return
		}

		success := "Deployment successful; wait 10s before restart."
		if branch != data.Branch {
			success += fmt.Sprintf(" Make sure to return to main branch once done (e.g., `!deploy %s confirm`).", data.Branch)
		}

		session.ChannelMessageSend(message.ChannelID, success)
		sendDiscordWebhookMessage("success", branch)
	}()
}

func main() {
	config, err := getConfig()
	if err != nil {
		log.Fatalf("getConfig(): %v", err)
	}

	data = config

	if err := getDictionary(&Commands); err != nil {
		log.Fatalf("getDictionary(): %v", err)
	}

	session, err := discordgo.New("Bot " + data.Token)
	if err != nil {
		log.Fatalf("discordgo.New(): %v", err)
	}

	session.AddHandler(deploy)
	session.Identify.Intents = discordgo.IntentGuilds | discordgo.IntentGuildModeration | discordgo.IntentGuildMembers | discordgo.IntentGuildMessages | discordgo.IntentMessageContent

	if err := session.Open(); err != nil {
		log.Fatalf("session.Open(): %v", err)
	}

	log.Printf("%s#%s is ready!", session.State.User.Username, session.State.User.Discriminator)

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt)
	<-stop

	log.Println("Shutdown complete.")
	if err := session.Close(); err != nil {
		log.Fatalf("session.Close(): %v", err)
	}
}

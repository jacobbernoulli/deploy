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
	Environment          string `env:"ENVIRONMENT"`
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

func sendDiscordWebhookMessage(status, branch string, author string) {
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
						"value":  data.Environment,
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
					"text": "User ID: " + author,
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
		str := val.Type().Field(i).Tag.Get("env")
		value, input := os.LookupEnv(str)
		if !input || strings.TrimSpace(value) == "" {
			return nil, fmt.Errorf("missing environment variable: %s", str)
		}
		val.Field(i).SetString(value)
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
	member, err := session.GuildMember(message.GuildID, message.Author.ID)
	if err != nil || !strings.HasPrefix(message.Content, "!") || message.Author.Bot || message.ChannelID != data.DeploymentChannel || !slices.Contains(member.Roles, data.DeploymentRole) {
		return
	}

	args := strings.Fields(strings.TrimPrefix(message.Content, "!"))
	if len(args) < 3 {
		session.ChannelMessageSend(message.ChannelID, "Missing fields - !deploy <branch> <key>")
		return
	}

	command, branch, key := strings.ToLower(args[0]), strings.ToLower(args[1]), args[2]
	if command != "deploy" {
		return
	}

	if _, ok := Commands[key]; !ok {
		session.ChannelMessageSend(message.ChannelID, fmt.Sprintf("Invalid key name `(%s)` specified.", key))
		return
	}

	if !regexp.MustCompile(`^[a-zA-Z0-9_-]+$`).MatchString(branch) || branch != data.Branch {
		session.ChannelMessageSend(message.ChannelID, fmt.Sprintf("Invalid branch `(%s)` specified.", branch))
		sendDiscordWebhookMessage("failed", branch, message.Author.ID)
		return
	}

	msg, err := session.ChannelMessageSend(message.ChannelID, "Deploying ongoing...")
	if err != nil {
		return
	}

	go func() {
		command := strings.ReplaceAll(strings.ReplaceAll(Commands[key], "${LOCATION}", data.DeploymentLocation), "${BRANCH}", branch)

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()

		output, err := exec.CommandContext(ctx, "bash", "-c", command).CombinedOutput()
		if err != nil {
			session.ChannelMessageEdit(message.ChannelID, msg.ID, fmt.Sprintf("Deployment failed: `%s`", err.Error()))
			log.Printf("cmd.CombinedOutput(): %v\n%s", err, string(output))
			return
		}

		session.ChannelMessageEdit(message.ChannelID, msg.ID, "Deployment successful, wait at least 10s if you need to restart.")
		sendDiscordWebhookMessage("success", branch, message.Author.ID)
		log.Printf("Deployment successful. Username: %s (%s) - Branch: %s - Executed: %s", message.Author.Username, message.Author.ID, branch, command)
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

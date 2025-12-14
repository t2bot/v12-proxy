package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"net/url"
	"os"
	"runtime/debug"

	"github.com/kelseyhightower/envconfig"
	"github.com/matrix-org/gomatrixserverlib"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

func init() {
	log.SetOutput(os.Stdout)
	log.SetPrefix("[v12 proxy] ")
	log.SetFlags(log.LstdFlags | log.Lshortfile | log.Lmicroseconds)

	version := "<unknown>"
	if build, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range build.Settings {
			if setting.Key == "vcs.revision" {
				version = setting.Value
				return
			}
		}
	}
	log.Println("Version:", version)
}

var maxPowerLevel = int64(math.Pow(2, 53) - 1)

type config struct {
	BindAddress   string `envconfig:"BIND_ADDRESS" default:":8080"`
	DownstreamUrl string `envconfig:"DOWNSTREAM_URL" default:"http://localhost:8008"`
}

var c = config{}

func main() {
	err := envconfig.Process("v12", &c)
	if err != nil {
		log.Fatal(err)
	}

	http.DefaultServeMux.HandleFunc("/_matrix/client/{endpointVersion}/rooms/{roomId}/m.room.power_levels", handleGetPowerLevels)
	http.DefaultServeMux.HandleFunc("/_matrix/client/{endpointVersion}/rooms/{roomId}/m.room.power_levels/", handleGetPowerLevels)
	http.DefaultServeMux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		log.Println("REJECT", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"errcode":"M_UNRECOGNIZED","error":"Not found"}`))
	})

	log.Println("Listening on", c.BindAddress)
	err = http.ListenAndServe(c.BindAddress, nil)
	if err != nil {
		log.Fatal(err)
	}
}

func handleGetPowerLevels(w http.ResponseWriter, r *http.Request) {
	log.Println("-->", r.Method, r.URL.Path)

	// Technically, we should respond to other methods too
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "M_UNRECOGNIZED", "Method not allowed")
		return
	}

	roomId := r.PathValue("roomId")

	createEventRaw, err := getStateEvent(r.Context(), r.Header.Get("Authorization"), roomId, "m.room.create")
	if err != nil {
		log.Println("Failed to get room create event:", err)
		writeError(w, http.StatusInternalServerError, "M_UNKNOWN", "Failed to get room create event")
		return
	}

	powerLevelsRaw, err := getStateEvent(r.Context(), r.Header.Get("Authorization"), roomId, "m.room.power_levels")
	if err != nil {
		log.Println("Failed to get room power levels event:", err)
		writeError(w, http.StatusInternalServerError, "M_UNKNOWN", "Failed to get room power levels event")
		return
	}

	likelyRoomVersion := "1"
	verStr := gjson.Get(createEventRaw, "content.room_version")
	if verStr.Exists() {
		likelyRoomVersion = verStr.String()
	}

	ver, err := gomatrixserverlib.GetRoomVersion(gomatrixserverlib.RoomVersion(likelyRoomVersion))
	if err != nil {
		log.Println("Failed to get room version:", err)
		writeError(w, http.StatusInternalServerError, "M_UNKNOWN", "Failed to get room version")
		return
	}

	log.Printf("Room '%s' is ~version '%s'", roomId, likelyRoomVersion)

	if ver.PrivilegedCreators() {
		users := make(map[string]int64)

		usersVal := gjson.Get(powerLevelsRaw, "content.users")
		if usersVal.Exists() {
			m := usersVal.Map()
			for k, v := range m {
				users[k] = v.Int()
			}
		}

		creator := gjson.Get(createEventRaw, "sender").String()
		users[creator] = maxPowerLevel
		log.Printf("Adding creator '%s' to power levels", creator)

		additionalCreators := gjson.Get(createEventRaw, "content.additional_creators")
		if additionalCreators.Exists() {
			for _, userId := range additionalCreators.Array() {
				log.Printf("Adding additional creator '%s' to power levels", userId.String())
				users[userId.String()] = maxPowerLevel
			}
		}

		powerLevelsRaw, err = sjson.Set(powerLevelsRaw, "content.users", users)
		if err != nil {
			log.Println("Failed to set users:", err)
			writeError(w, http.StatusInternalServerError, "M_UNKNOWN", "Failed to set users")
			return
		}
	}

	if r.URL.Query().Get("format") == "event" {
		_, _ = w.Write([]byte(powerLevelsRaw))
	} else {
		_, _ = w.Write([]byte(gjson.Get(powerLevelsRaw, "content").Raw))
	}
}

func writeError(w http.ResponseWriter, status int, errcode string, message string) {
	b, err := json.Marshal(map[string]string{"errcode": errcode, "error": message})
	if err != nil {
		log.Println("Error marshalling error:", err)
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"errcode":"M_UNKNOWN","error":"Unknown error marshalling error"}`))
		return
	}

	w.WriteHeader(status)
	_, _ = w.Write(b)
}

func getStateEvent(ctx context.Context, auth string, roomId string, eventType string) (string, error) {
	joined, err := url.JoinPath(c.DownstreamUrl, fmt.Sprintf("/_matrix/client/v3/rooms/%s/state/%s/", url.PathEscape(roomId), url.PathEscape(eventType)))
	if err != nil {
		return "", err
	}
	req, err := http.NewRequest(http.MethodGet, joined+"?format=event", nil)
	if err != nil {
		return "", err
	}
	log.Println("<--", req.Method, req.URL.Path)
	req.Header.Set("Authorization", auth)

	resp, err := http.DefaultClient.Do(req.WithContext(ctx))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected status code %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

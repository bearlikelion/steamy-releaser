package steam

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

var storeAPIBase = "https://store.steampowered.com/api/appdetails"

// AppName asks Valve's public store API for an app's name; unreleased apps have none.
func AppName(ctx context.Context, appID uint64) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	id := strconv.FormatUint(appID, 10)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, storeAPIBase+"?filters=basic&appids="+id, nil)
	if err != nil {
		return "", err
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("Steam store returned %s", response.Status)
	}
	var body map[string]struct {
		Success bool `json:"success"`
		Data    struct {
			Name string `json:"name"`
		} `json:"data"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(nil, response.Body, 1<<20)).Decode(&body); err != nil {
		return "", err
	}
	return body[id].Data.Name, nil
}

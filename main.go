package main

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"regexp"
	"strconv"
	"time"

	// "log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/cheggaaa/pb/v3"
)

var log *zap.SugaredLogger
var pbTmpl = pb.Full

func newLoggerConfig() zap.Config {
	encoderCfg := zapcore.EncoderConfig{
		// Keys can be anything except the empty string.
		TimeKey:  "ts",
		LevelKey: "level",
		//	NameKey:        "logger",
		CallerKey:     "caller",
		MessageKey:    "msg",
		StacktraceKey: "stacktrace",
		LineEnding:    zapcore.DefaultLineEnding,
		EncodeLevel:   zapcore.CapitalColorLevelEncoder,
		EncodeTime: func(ts time.Time, encoder zapcore.PrimitiveArrayEncoder) {
			encoder.AppendString(ts.Local().Format(time.RFC3339))
		},
		EncodeDuration: zapcore.SecondsDurationEncoder,
		EncodeCaller:   zapcore.ShortCallerEncoder,
	}
	cfg := zap.Config{
		Level:    zap.NewAtomicLevelAt(zap.InfoLevel),
		Encoding: "console",
		Sampling: &zap.SamplingConfig{
			Initial:    100,
			Thereafter: 100,
		},
		EncoderConfig:    encoderCfg,
		OutputPaths:      []string{"stdout"},
		ErrorOutputPaths: []string{"stderr"},
	}
	return cfg
}

func init() {
	cfg := newLoggerConfig()
	logger, err := cfg.Build()
	if err != nil {
		panic(err)
	}
	defer logger.Sync()
	log = logger.Sugar()
}

const userAgentDefault = "Mozilla/5.0 (X11; Linux x86_64; rv:89.0) Gecko/20100101 Firefox/89.0"

type HTTPClient struct {
	userAgent    string
	smsessCookie string
}

func NewHTTPClient(userAgent, smsessCookie string) *HTTPClient {
	return &HTTPClient{userAgent: userAgent, smsessCookie: smsessCookie}
}

const retries = 3

func (c *HTTPClient) Req(url string) ([]byte, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Cookie", fmt.Sprintf("SMSESS=%v", c.smsessCookie))

	attempt := 0
	for {
		client := &http.Client{}
		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()

		if resp.StatusCode != 200 {
			if 500 <= resp.StatusCode && resp.StatusCode < 600 {
				if attempt == retries {
					return nil, fmt.Errorf("reached max req attempts; resp.StatusCode is %v", resp.StatusCode)
				}
				attempt += 1
				log.Warnf("res.StatusCode is %v, trying again (attempt %v)", resp.StatusCode, attempt)
				time.Sleep(500 * time.Millisecond)
				continue
			} else {
				return nil, fmt.Errorf("resp.StatusCode is %v", resp.StatusCode)
			}
		}
		return ioutil.ReadAll(resp.Body)
	}

}

func (c *HTTPClient) ReqJSON(url string, v interface{}) error {
	content, err := c.Req(url)
	// DBG BEGIN
	// fmt.Printf("---\n")
	// var val interface{}
	// json.Unmarshal(content, &val)
	// j, _ := json.MarshalIndent(val, "", "  ")
	// fmt.Printf("%v\n", string(j))
	// fmt.Printf("---\n")
	// DBG END
	if err != nil {
		return err
	}
	return json.Unmarshal(content, v)
}

type Node struct {
	Name                  string
	Type                  string
	UrlName               string
	DateAdded             string
	DateModified          string
	EffectiveSecurityType string
	NodeID                string
	UrlPath               string
	Uri                   string
	// Uris                  interface{}
	Uris struct {
		Album struct {
			Uri string
		}
	}
}

type Image struct {
	Title            string
	FileName         string
	DateTimeUploaded string
	DateTimeOriginal string
	ImageKey         string
	ArchivedUri      string
	ArchivedMD5      string
	Uris             struct {
		LargestImage struct {
			Uri string
		}
	}
}

type ImageSize struct {
	LargestImage struct {
		Url string
		MD5 string
	}
}

type FolderResponse struct {
	Response struct {
		Node       []Node
		AlbumImage []Image
		Pages      struct {
			Total     int
			Start     int
			Count     int
			FirstPage string
			LastPage  string
		}
	}
	Expansions map[string]ImageSize
}

type ImageV1 struct {
	ImageID     int
	ImageKey    string
	Index       int
	GalleryUrl  string
	URLFilename string
}

type AlbumResponseV1 struct {
	Images     []Image
	Pagination struct {
		PageNumber    int
		PageSize      int
		TotalItems    int
		TotalPages    int
		ItemsOnPage   int
		PreviousImage int
		NextImage     int
	}
}

type AlbumURLBuilder struct {
	APIKey  string
	BaseURL string
}

func (b *AlbumURLBuilder) URL(albumID string, start int) (string, error) {
	albumURL, err := url.Parse(fmt.Sprintf("%v/api/v2/album/%v!images", b.BaseURL, albumID))
	if err != nil {
		return "", fmt.Errorf("Unable to parse baseURL: %w", err)
	}

	params := url.Values{}
	params.Add("APIKey", b.APIKey)
	params.Add("_accept", "application/json")
	params.Add("Type", "Folder Album Page")
	params.Add("SortMethod", "Organizer")
	params.Add("SortDirection", "Descending")
	params.Add("count", "50")
	params.Add("start", fmt.Sprintf("%v", start))
	params.Add("_expand", "LargestImage")
	albumURL.RawQuery = params.Encode()
	return albumURL.String(), nil
}

const unknownTotal = 0xffff

func imageHashURL(cli *HTTPClient, album *FolderResponse, image *Image) (string, string) {
	if image.ArchivedUri != "" {
		return image.ArchivedMD5, image.ArchivedUri
	}
	largestImage := album.Expansions[image.Uris.LargestImage.Uri]
	return largestImage.LargestImage.MD5, largestImage.LargestImage.Url
}

func loopAlbum(cli *HTTPClient, ab *AlbumURLBuilder, path, albumID string) {
	start := 1
	total := unknownTotal
	log.Infof("Requesting album at %q with albumID %v", path, albumID)

	var bar *pb.ProgressBar
	re := regexp.MustCompile(`^([^0-9]*)([0-9]+).jpg$`)
	type Session struct {
		Count   int
		Indexes map[int]bool
	}
	sessions := make(map[string]Session)
	for start < total {
		albumURL, err := ab.URL(albumID, start)
		if err != nil {
			log.Errorf("Can't build album URL: %v", err)
			continue
		}

		var album FolderResponse
		if err := cli.ReqJSON(albumURL, &album); err != nil {
			log.Errorf("Unable to get album at url %v: %v", albumURL, err)
			continue
		}
		if total == unknownTotal {
			total = album.Response.Pages.Total
			bar = pb.ProgressBarTemplate(pbTmpl).Start(total)
		}
		start += album.Response.Pages.Count
		for _, image := range album.Response.AlbumImage {
			var sessionName string
			var index int
			match := re.FindStringSubmatch(image.FileName)
			if match == nil {
				sessionName = strings.TrimSuffix(image.FileName, ".jpg")
				index = 0
			} else {
				sessionName = match[1]
				index, err = strconv.Atoi(match[2])
				if err != nil {
					log.Fatalf("can't convert %q to int: %v", match[2], err)
				}
			}
			if _, ok := sessions[sessionName]; !ok {
				sessions[sessionName] = Session{
					Count:   0,
					Indexes: make(map[int]bool),
				}
			}
			if _, ok := sessions[sessionName].Indexes[index]; !ok {
				sessions[sessionName].Indexes[index] = true
			} else {
				sessions[sessionName] = Session{
					Count:   sessions[sessionName].Count + 1,
					Indexes: make(map[int]bool),
				}
				sessions[sessionName].Indexes[index] = true
			}
			prefix := fmt.Sprintf("%02d", sessions[sessionName].Count)
			fileName := fmt.Sprintf("%v_%v", prefix, image.FileName)
			filePath := filepath.Join(path, fileName)
			imageHash, imageURL := imageHashURL(cli, &album, &image)
			hash := md5.New()
			file, err := os.Open(filePath)
			if err == nil {
				if _, err := io.Copy(hash, file); err != nil {
					log.Errorf("can'read open file %v: %v", filePath, err)
					continue
				}
				fileHash := hex.EncodeToString(hash.Sum(nil)[:16])
				if fileHash == imageHash {
					bar.Increment()
					continue
				}
				log.Infof("hash mismatch for existing file %v, downloading again", filePath)

			} else if !os.IsNotExist(err) && err != nil {
				log.Errorf("can't open file %v: %v", filePath, err)
				continue
			}

			imgData, err := cli.Req(imageURL)
			if err != nil {
				log.Errorf("can't request %v: %v", image.ArchivedUri, err)
				continue
			}
			if err := ioutil.WriteFile(filePath, imgData, 0644); err != nil {
				log.Errorf("can't write image file %v: %v", filePath, err)
				continue
			}
			bar.Increment()
		}
	}
	bar.Finish()
}

type FolderURLBuilder struct {
	BaseURL string
	APIKey  string
}

func (b *FolderURLBuilder) URL(nodeID string, start int) (string, error) {
	folderURL, err := url.Parse(fmt.Sprintf("%v/api/v2/node/%v!children", b.BaseURL, nodeID))
	if err != nil {
		return "", fmt.Errorf("Unable to parse baseURL: %w", err)
	}

	params := url.Values{}
	params.Add("APIKey", b.APIKey)
	params.Add("_accept", "application/json")
	params.Add("Type", "Folder Album Page")
	params.Add("SortMethod", "Organizer")
	params.Add("SortDirection", "Descending")
	params.Add("count", "50")
	params.Add("start", fmt.Sprintf("%v", start))
	folderURL.RawQuery = params.Encode()
	return folderURL.String(), nil
}

func loopFolder(cli *HTTPClient, fb *FolderURLBuilder, path, nodeID string) {
	start := 1
	total := 0xffff
	// Loop Folder
	log.Infof("Requesting folder at %q with nodeID %v", path, nodeID)
	for start < total {
		folderURL, err := fb.URL(nodeID, start)
		if err != nil {
			log.Errorf("Can't build folder URL: %v", err)
			continue
		}

		var folder FolderResponse
		if err := cli.ReqJSON(folderURL, &folder); err != nil {
			log.Errorf("Unable to get folder at url %v: %v", folderURL, err)
			continue
		}

		for _, node := range folder.Response.Node {
			subPath := filepath.Join(path, node.Name)
			if err := os.MkdirAll(subPath, 0755); err != nil {
				log.Errorf("cannot mkdir subPath %v: %v", subPath, err)
				continue
			}
			switch node.Type {
			case "Folder":
				loopFolder(cli, fb, subPath, node.NodeID)
			case "Album":
				ab := AlbumURLBuilder{APIKey: fb.APIKey, BaseURL: fb.BaseURL}
				albumID := strings.TrimPrefix(node.Uris.Album.Uri, "/api/v2/album/")
				loopAlbum(cli, &ab, subPath, albumID)
			default:
				log.Errorf("Unexpected node.Type = %v", node.Type)
				continue
			}
		}
		total = folder.Response.Pages.Total
		start += folder.Response.Pages.Count
	}
}

func main() {
	var apiKey string
	var smsessCookie string
	var nodeID string
	var baseURL string
	flag.StringVar(&apiKey, "apiKey", "", "APIKey")
	flag.StringVar(&smsessCookie, "smsessCookie", "", "SMSESS Cookie")
	flag.StringVar(&nodeID, "nodeID", "", "main nodeID")
	flag.StringVar(&baseURL, "baseURL", "", "base URL")
	flag.Parse()

	if apiKey == "" {
		log.Fatalf("Missing apiKey flag")
	}
	if smsessCookie == "" {
		log.Fatalf("Missing smsessCookie flag")
	}
	if nodeID == "" {
		log.Fatalf("Missing nodeID flag")
	}
	if baseURL == "" {
		log.Fatalf("Missing baseURL flag")
	}

	cli := NewHTTPClient(userAgentDefault, smsessCookie)

	fb := FolderURLBuilder{APIKey: apiKey, BaseURL: baseURL}
	loopFolder(cli, &fb, ".", nodeID)
}

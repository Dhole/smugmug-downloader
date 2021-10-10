package main

import (
	"encoding/json"
	"flag"
	"fmt"
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

// Main url:
// curl 'https://modelindexdatabase.smugmug.com/api/v2/node/4nXMLW!children?_shorturis&APIKey=W0g9oqdOrzuhEpIQ2qaTXimrzsfryKSZ&_accept=application%2Fjson&_verbose&Type=Folder%20Album%20Page&SortMethod=Organizer&SortDirection=Descending&count=49&start=1&_expand=HighlightImage%3F_shorturis%3D.ImageSizeDetails%3F_shorturis%3D%2CHighlightImage%3F_shorturis%3D.ImageAlbum%3F_shorturis%3D%2CHighlightImage%3F_shorturis%3D.PointOfInterestCrops%3F_shorturis%3D' -H 'User-Agent: Mozilla/5.0 (X11; Linux x86_64; rv:92.0) Gecko/20100101 Firefox/92.0' -H 'Accept: application/json' -H 'Accept-Language: en-US,en;q=0.5' --compressed -H 'Connection: keep-alive' -H 'Referer: https://modelindexdatabase.smugmug.com/browse' -H 'Cookie: Sreferrer=https%3A%2F%2Fonairvideo.com%2F; SMSESS=8dcca98ca4fe18d84bb9d2feb2dcd3f3; sp=eb736d03-932d-4795-a1e5-ed4fc68f04f4' -H 'Sec-Fetch-Dest: empty' -H 'Sec-Fetch-Mode: cors' -H 'Sec-Fetch-Site: same-origin' -H 'DNT: 1' -H 'Sec-GPC: 1' -H 'TE: trailers'
//
// Croquis-Cafe-Model-Photo-Database:
// curl 'https://modelindexdatabase.smugmug.com/api/v2/node/HhLVs7!children?_shorturis&APIKey=W0g9oqdOrzuhEpIQ2qaTXimrzsfryKSZ&_accept=application%2Fjson&_verbose&Type=Folder%20Album%20Page&SortMethod=Organizer&SortDirection=Descending&count=49&start=1&_expand=HighlightImage%3F_shorturis%3D.ImageSizeDetails%3F_shorturis%3D%2CHighlightImage%3F_shorturis%3D.ImageAlbum%3F_shorturis%3D%2CHighlightImage%3F_shorturis%3D.PointOfInterestCrops%3F_shorturis%3D' -H 'User-Agent: Mozilla/5.0 (X11; Linux x86_64; rv:92.0) Gecko/20100101 Firefox/92.0' -H 'Accept: application/json' -H 'Accept-Language: en-US,en;q=0.5' --compressed -H 'Connection: keep-alive' -H 'Referer: https://modelindexdatabase.smugmug.com/Croquis-Cafe-Model-Photo-Database' -H 'Cookie: Sreferrer=https%3A%2F%2Fonairvideo.com%2F; SMSESS=8dcca98ca4fe18d84bb9d2feb2dcd3f3; sp=eb736d03-932d-4795-a1e5-ed4fc68f04f4' -H 'Sec-Fetch-Dest: empty' -H 'Sec-Fetch-Mode: cors' -H 'Sec-Fetch-Site: same-origin' -H 'DNT: 1' -H 'Sec-GPC: 1' -H 'Cache-Control: max-age=0' -H 'TE: trailers'
//
// Croquis-Cafe-Model-Photo-Database/Samantha:
// curl 'https://modelindexdatabase.smugmug.com/services/api/json/1.4.0/?galleryType=album&albumId=264777298&albumKey=vqHdfn&nodeId=ncxM7d&PageNumber=0&imageId=0&imageKey=&returnModelList=true&PageSize=30&imageSizes=L%2CXL&method=rpc.gallery.getalbum' -H 'User-Agent: Mozilla/5.0 (X11; Linux x86_64; rv:92.0) Gecko/20100101 Firefox/92.0' -H 'Accept: application/json' -H 'Accept-Language: en-US,en;q=0.5' --compressed -H 'X-Requested-With: XMLHttpRequest' -H 'sentry-trace: 01db2a844b9c47c580e3357619725f04-944e01c427817310-0' -H 'Connection: keep-alive' -H 'Referer: https://modelindexdatabase.smugmug.com/Croquis-Cafe-Model-Photo-Database/Samantha/' -H 'Cookie: Sreferrer=https%3A%2F%2Fonairvideo.com%2F; SMSESS=8dcca98ca4fe18d84bb9d2feb2dcd3f3; sp=eb736d03-932d-4795-a1e5-ed4fc68f04f4' -H 'Sec-Fetch-Dest: empty' -H 'Sec-Fetch-Mode: cors' -H 'Sec-Fetch-Site: same-origin' -H 'DNT: 1' -H 'Sec-GPC: 1' -H 'Cache-Control: max-age=0' -H 'TE: trailers'
//
// Croquis-Cafe-Model-Photo-Database/Samantha (1)
// https://photos.smugmug.com/photos/i-xRxXBPQ/0/X4/i-xRxXBPQ-X4.jpg
// Croquis-Cafe-Model-Photo-Database/Samantha (2) Original size
// https://photos.smugmug.com/photos/i-zFF7RGz/0/O/i-zFF7RGz-O.jpg

// type AlbumURLBuilderV1 struct {
// }
//
// func (b *AlbumURLBuilderV1) URL(nodeID string, start int) string {
// 	baseURL := "https://modelindexdatabase.smugmug.com"
// 	albumURL, err := url.Parse(fmt.Sprintf("%v/services/api/json/1.4.0/", baseURL))
// 	if err != nil {
// 		log.Fatalf("Unable to parse baseURL: %v", err)
// 	}
//
// 	params := url.Values{}
// 	params.Add("galleryType", "album")
// 	params.Add("albumId", "264777298")
// 	params.Add("albumKey", "vqHdfn")
// 	params.Add("nodeId", nodeID)
// 	params.Add("PageNumber", fmt.Sprintf("%v", start))
// 	params.Add("imageId", "0")
// 	params.Add("imageKey", "")
// 	params.Add("returnModelList", "true")
// 	params.Add("PageSize", "32")
// 	params.Add("imageSizes", "O")
// 	params.Add("method", "rpc.gallery.getalbum")
//
// 	albumURL.RawQuery = params.Encode()
// 	return albumURL.String()
// }

type AlbumURLBuilder struct {
	APIKey string
}

func (b *AlbumURLBuilder) URL(albumID string, start int) (string, error) {
	baseURL := "https://modelindexdatabase.smugmug.com"
	albumURL, err := url.Parse(fmt.Sprintf("%v/api/v2/album/%v!images", baseURL, albumID))
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
	// params.Add("_expand", "HighlightImage?_shorturis=.ImageSizeDetails?_shorturis=,HighlightImage?_shorturis=.ImageAlbum?_shorturis=,HighlightImage?_shorturis=.PointOfInterestCrops?_shorturis=")
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
	// if path != "Croquis Cafe Model Photo Database/Unique" {
	// 	fmt.Printf("DBG Skipping...\n")
	// 	return
	// }

	var bar *pb.ProgressBar
	re := regexp.MustCompile(`^([^0-9]*)([0-9]+).jpg$`)
	// sessionName -> list of sessions with same name -> index in that session
	sessions := make(map[string][]map[int]bool)
	for start < total {
		albumURL, err := ab.URL(albumID, start)
		if err != nil {
			log.Errorf("Can't build album URL: %v", err)
			continue
		}
		// fmt.Printf("DBG url: %v\n", albumURL)

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
		// fmt.Printf("DBG %+v\n", album)
		for _, image := range album.Response.AlbumImage {
			// prefix := fmt.Sprintf("%04d", start+i)
			// t := time.UnixMilli(0)
			// var err error
			// if image.DateTimeOriginal != "" {
			// 	t, err = time.Parse(time.RFC3339, image.DateTimeOriginal)
			// 	if err != nil {
			// 		log.Warnf("can't parse image.DateTimeOriginal: %v", err)
			// 	}
			// } else if image.DateTimeUploaded != "" {
			// 	t, err = time.Parse(time.RFC3339, image.DateTimeUploaded)
			// 	if err != nil {
			// 		log.Warnf("can't parse image.DateTimeOriginal: %v", err)
			// 	}
			// }
			// if t != time.UnixMilli(0) && err == nil {
			// 	prefix = fmt.Sprintf("%010v", t.Unix())
			// }
			var session string
			var index int
			match := re.FindStringSubmatch(image.FileName)
			if match == nil {
				session = strings.TrimSuffix(image.FileName, ".jpg")
				index = 0
			} else {
				session = match[1]
				index, err = strconv.Atoi(match[2])
				if err != nil {
					log.Fatalf("can't convert %q to int: %v", match[2], err)
				}
			}
			if _, ok := sessions[session]; !ok {
				sessions[session] = make([]map[int]bool, 1)
				sessions[session][0] = make(map[int]bool)
			}
			last := sessions[session][len(sessions[session])-1]
			if _, ok := last[index]; !ok {
				last[index] = true
			} else {
				sessions[session] = append(sessions[session], make(map[int]bool))
				last := sessions[session][len(sessions[session])-1]
				last[index] = true
			}
			prefix := fmt.Sprintf("%02d", len(sessions[session])-1)
			sessionID := fmt.Sprintf("%v_%v", prefix, session)
			fileName := fmt.Sprintf("%v_%v", prefix, image.FileName)
			fmt.Printf("image.FileName: %q sessionID: %q index: %v -> %v\n", image.FileName, sessionID, index, fileName)
			// filePath := filepath.Join(path, fileName)
			// // fmt.Printf("DBG %v Image %v -> %v\n", start+i, image.FileName, image.ArchivedUri)
			// imageHash, imageURL := imageHashURL(cli, &album, &image)
			// hash := md5.New()
			// file, err := os.Open(filePath)
			// if err == nil {
			// 	if _, err := io.Copy(hash, file); err != nil {
			// 		log.Errorf("can'read open file %v: %v", filePath, err)
			// 		continue
			// 	}
			// 	fileHash := hex.EncodeToString(hash.Sum(nil)[:16])
			// 	if fileHash == imageHash {
			// 		bar.Increment()
			// 		continue
			// 	}
			// 	log.Infof("hash mismatch for existing file %v, downloading again", filePath)

			// } else if !os.IsNotExist(err) && err != nil {
			// 	log.Errorf("can't open file %v: %v", filePath, err)
			// 	continue
			// }

			// imgData, err := cli.Req(imageURL)
			// if err != nil {
			// 	log.Errorf("can't request %v: %v", image.ArchivedUri, err)
			// 	continue
			// }
			// if err := ioutil.WriteFile(filePath, imgData, 0644); err != nil {
			// 	log.Errorf("can't write image file %v: %v", filePath, err)
			// 	continue
			// }
			// bar.Increment()
		}
	}
	bar.Finish()
}

type FolderURLBuilder struct {
	APIKey string
}

func (b *FolderURLBuilder) URL(nodeID string, start int) (string, error) {
	baseURL := "https://modelindexdatabase.smugmug.com"
	// folderURL := fmt.Sprintf("%v/api/v2/node/%v!children", baseURL, nodeID)
	folderURL, err := url.Parse(fmt.Sprintf("%v/api/v2/node/%v!children", baseURL, nodeID))
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
	// params.Add("_expand", "HighlightImage?_shorturis=.ImageSizeDetails?_shorturis=,HighlightImage?_shorturis=.ImageAlbum?_shorturis=,HighlightImage?_shorturis=.PointOfInterestCrops?_shorturis=")
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
		// fmt.Printf("DBG %+v\n", folder.Response.Pages)

		for _, node := range folder.Response.Node {
			// fmt.Printf("DBG %v Node %v (%v) -> %v uris:%+v\n", start+i, node.Name, node.Type, node.NodeID, node.Uris)
			subPath := filepath.Join(path, node.Name)
			if err := os.MkdirAll(subPath, 0755); err != nil {
				log.Errorf("cannot mkdir subPath %v: %v", subPath, err)
				continue
			}
			switch node.Type {
			case "Folder":
				loopFolder(cli, fb, subPath, node.NodeID)
			case "Album":
				ab := AlbumURLBuilder{APIKey: fb.APIKey}
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

const mainNodeID = "4nXMLW"

func main() {
	var apiKey string
	var smsessCookie string
	flag.StringVar(&apiKey, "apiKey", "", "APIKey")                    // Example: "W0g9oqdOrzuhEpIQ2qaTXimrzsfryKSZ"
	flag.StringVar(&smsessCookie, "smsessCookie", "", "SMSESS Cookie") // Example: "8dcca98ca4fe18d84bb9d2feb2dcd3f3"
	flag.Parse()

	if apiKey == "" {
		log.Fatalf("Missing apiKey flag")
	}
	if smsessCookie == "" {
		log.Fatalf("Missing smsessCookie flag")
	}

	cli := NewHTTPClient(userAgentDefault, smsessCookie)

	// fb := FolderURLBuilder{APIKey: apiKey}
	// // nodeID := "4nXMLW" // main nodeID
	// nodeID := "HhLVs7"
	// loopFolder(cli, &fb, ".", mainNodeID)

	ab := AlbumURLBuilder{APIKey: apiKey}
	albumID := "W8hVzH"
	loopAlbum(cli, &ab, "Croquis Cafe Model Photo Database/Helen Troy", albumID)
}

/*

https://pkg.go.dev/github.com/kkdai/youtube/v2/

go get github.com/kkdai/youtube/v2@master

GoGet
GoFmt
GoBuildNull

*/

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	ytdl "github.com/kkdai/youtube/v2"
	"golang.org/x/exp/slices"
	yaml "gopkg.in/yaml.v3"

	"github.com/shoce/tg"
)

const (
	NL   = "\n"
	SPAC = "    "

	BEAT = time.Duration(24) * time.Hour / 1000
)

type TgZeConfig struct {
	YssUrl string `yaml:"-"`

	DEBUG bool `yaml:"DEBUG"`

	Interval time.Duration `yaml:"Interval"`

	TgApiUrlBase string `yaml:"TgApiUrlBase"` // = "https://api.telegram.org"

	TgToken            string  `yaml:"TgToken"`
	TgZeChatId         int64   `yaml:"TgZeChatId"`
	TgUpdateLog        []int64 `yaml:"TgUpdateLog,flow"`
	TgUpdateLogMaxSize int     `yaml:"TgUpdateLogMaxSize"` // = 1080

	TgCommandChannels             string `yaml:"TgCommandChannels"`
	TgCommandChannelsPromoteAdmin string `yaml:"TgCommandChannelsPromoteAdmin"`

	TgQuest1    string `yaml:"TgQuest1"`
	TgQuest1Key string `yaml:"TgQuest1Key"`
	TgQuest2    string `yaml:"TgQuest2"`
	TgQuest2Key string `yaml:"TgQuest2Key"`
	TgQuest3    string `yaml:"TgQuest3"`
	TgQuest3Key string `yaml:"TgQuest3Key"`

	TgAllChannelsChatIds []int64 `yaml:"TgAllChannelsChatIds,flow"`

	TgMaxFileSizeBytes int64 `yaml:"TgMaxFileSizeBytes"` // = 47 << 20
	TgAudioBitrateKbps int64 `yaml:"TgAudioBitrateKbps"` // = 60

	FfmpegPath          string   `yaml:"FfmpegPath"`          // = "/bin/ffmpeg"
	FfmpegGlobalOptions []string `yaml:"FfmpegGlobalOptions"` // = []string{"-v", "error"}

	YtKey        string `yaml:"YtKey"`
	YtMaxResults int64  `yaml:"YtMaxResults"` // = 50

	YtHttpClientUserAgent string `yaml:"YtHttpClientUserAgent"` // = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.4 Safari/605.1.15"

	// https://golang.org/s/re2syntax
	// (?:re)	non-capturing group
	// TODO add support for https://www.youtube.com/watch?&list=PL5Qevr-CpW_yZZjYspehnFc-QRKQMCKHB&v=1nzx7O7ndfI&index=34
	YtRe     string `yaml:"YtRe"`     // = `(?:youtube.com/watch\?v=|youtu.be/|youtube.com/shorts/|youtube.com/live/)([0-9A-Za-z_-]+)`
	YtListRe string `yaml:"YtListRe"` // = `youtube.com/playlist\?list=([0-9A-Za-z_-]+)`

	YtDownloadLanguages []string `yaml:"YtDownloadLanguages"` // = []string{"english", "german", "russian", "ukrainian"}
}

var (
	Ctx context.Context

	HttpClient = &http.Client{}

	Config TgZeConfig

	YtdlCl         ytdl.Client
	YtRe, YtListRe *regexp.Regexp
)

func init() {
	Ctx = context.TODO()

	if v := os.Getenv("YssUrl"); v != "" {
		Config.YssUrl = v
	}
	if Config.YssUrl == "" {
		log("ERROR YssUrl empty")
		os.Exit(1)
	}
	log("YssUrl==%v", Config.YssUrl)

	if err := Config.Get(); err != nil {
		log("ERROR Config.Get: %v", err)
		os.Exit(1)
	}

	if Config.DEBUG {
		log("DEBUG==true")
	}

	log("Interval==%v", Config.Interval)
	if Config.Interval == 0 {
		log("ERROR Interval empty")
		os.Exit(1)
	}

	var err error
	YtRe, err = regexp.Compile(Config.YtRe)
	if err != nil {
		log("ERROR Compile YtRe `%s`: %s", Config.YtRe, err)
		os.Exit(1)
	}
	YtListRe, err = regexp.Compile(Config.YtListRe)
	if err != nil {
		log("ERROR Compile YtListRe `%s`: %s", Config.YtListRe, err)
		os.Exit(1)
	}

	if Config.TgToken == "" {
		log("ERROR TgToken empty")
		os.Exit(1)
	}

	tg.ApiToken = Config.TgToken

	tg.ApiUrl = Config.TgApiUrlBase

	log("TgUpdateLog==%+v", Config.TgUpdateLog)

	if Config.TgCommandChannels == "" {
		log("ERROR TgCommandChannels empty")
		os.Exit(1)
	}

	if Config.TgCommandChannelsPromoteAdmin == "" {
		log("ERROR TgCommandChannelsPromoteAdmin empty")
		os.Exit(1)
	}

	if Config.YtKey == "" {
		log("ERROR YtKey empty")
		os.Exit(1)
	}

	log("FfmpegPath==`%s`", Config.FfmpegPath)
	log("FfmpegGlobalOptions==%+v", Config.FfmpegGlobalOptions)
}

func main() {
	sigterm := make(chan os.Signal, 1)
	signal.Notify(sigterm, syscall.SIGTERM)
	go func(sigterm chan os.Signal) {
		<-sigterm
		tg.SendMessage(tg.SendMessageRequest{
			ChatId: strconv.FormatInt(Config.TgZeChatId, 10),
			Text:   tg.Esc(fmt.Sprintf("%s: sigterm", os.Args[0])),
		})
		log("sigterm received")
		os.Exit(1)
	}(sigterm)

	for {
		t0 := time.Now()

		YtdlCl = ytdl.Client{HTTPClient: &http.Client{Transport: &UserAgentTransport{http.DefaultTransport, Config.YtHttpClientUserAgent}}}
		processTgUpdates()

		if dur := time.Now().Sub(t0); dur < Config.Interval {
			time.Sleep(Config.Interval - dur)
		}
	}

	return
}

type YtChannel struct {
	Id             string `json:"id"`
	ContentDetails struct {
		RelatedPlaylists struct {
			Uploads string `json:"uploads"`
		} `json:"relatedPlaylists"`
	} `json:"contentDetails"`
}

type YtChannelListResponse struct {
	PageInfo struct {
		TotalResults   int64 `json:"totalResults"`
		ResultsPerPage int64 `json:"resultsPerPage"`
	} `json:"pageInfo"`
	Items []YtChannel `json:"items"`
}

type YtPlaylistSnippet struct {
	Title        string `json:"title"`
	Description  string `json:"description"`
	PublishedAt  string `json:"publishedAt"`
	ChannelId    string `json:"channelId"`
	ChannelTitle string `json:"channelTitle"`
	Thumbnails   struct {
		Medium struct {
			Url string `json:"url"`
		} `json:"medium"`
		High struct {
			Url string `json:"url"`
		} `json:"high"`
		Standard struct {
			Url string `json:"url"`
		} `json:"standard"`
		MaxRes struct {
			Url string `json:"url"`
		} `json:"maxres"`
	} `json:"thumbnails"`
}

type YtPlaylist struct {
	Snippet        YtPlaylistSnippet `json:"snippet"`
	ContentDetails struct {
		ItemCount uint `json:"itemCount"`
	} `json:"contentDetails"`
}

type YtPlaylists struct {
	NextPageToken string `json:"nextPageToken"`
	PageInfo      struct {
		TotalResults   int64 `json:"totalResults"`
		ResultsPerPage int64 `json:"resultsPerPage"`
	} `json:"pageInfo"`
	Items []YtPlaylist
}

type YtPlaylistItemSnippet struct {
	Title        string `json:"title"`
	Description  string `json:"description"`
	PublishedAt  string `json:"publishedAt"`
	ChannelId    string `json:"channelId"`
	ChannelTitle string `json:"channelTitle"`
	PlaylistId   string `json:"playlistId"`
	Thumbnails   struct {
		Medium struct {
			Url string `json:"url"`
		} `json:"medium"`
		High struct {
			Url string `json:"url"`
		} `json:"high"`
		Standard struct {
			Url string `json:"url"`
		} `json:"standard"`
		MaxRes struct {
			Url string `json:"url"`
		} `json:"maxres"`
	} `json:"thumbnails"`
	Position   int64 `json:"position"`
	ResourceId struct {
		VideoId string `json:"videoId"`
	} `json:"resourceId"`
}

type YtPlaylistItem struct {
	Snippet YtPlaylistItemSnippet `json:"snippet"`
}

type YtPlaylistItems struct {
	NextPageToken string `json:"nextPageToken"`
	PageInfo      struct {
		TotalResults   int64 `json:"totalResults"`
		ResultsPerPage int64 `json:"resultsPerPage"`
	} `json:"pageInfo"`
	Items []YtPlaylistItem
}

type YtVideo struct {
	Id            string
	PlaylistId    string
	PlaylistIndex int64
	PlaylistSize  int64
	PlaylistTitle string
}

type UserAgentTransport struct {
	Transport http.RoundTripper
	UserAgent string
}

func (uat *UserAgentTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req.Header.Set("User-Agent", uat.UserAgent)
	return uat.Transport.RoundTrip(req)
}

func getJson(url string, target interface{}, respjson *string) (err error) {
	resp, err := HttpClient.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var respBody []byte
	respBody, err = io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("io.ReadAll: %w", err)
	}

	err = json.NewDecoder(bytes.NewBuffer(respBody)).Decode(target)
	if err != nil {
		return fmt.Errorf("json.Decoder.Decode: %w", err)
	}

	if Config.DEBUG {
		log("DEBUG getJson %s response ContentLength:%d Body:"+NL+"%s", url, resp.ContentLength, respBody)
	}
	if respjson != nil {
		*respjson = string(respBody)
	}

	return nil
}

func processTgUpdates() {
	var err error

	var tgdeleteMessages []tg.DeleteMessageRequest
	defer func(mm *[]tg.DeleteMessageRequest) {
		for _, cm := range *mm {
			tg.DeleteMessage(tg.DeleteMessageRequest{
				ChatId:    cm.ChatId,
				MessageId: cm.MessageId,
			})
		}
	}(&tgdeleteMessages)

	var updatesoffset int64
	if len(Config.TgUpdateLog) > 0 {
		updatesoffset = Config.TgUpdateLog[len(Config.TgUpdateLog)-1] + 1
	}

	var uu []tg.Update
	var respjson string
	uu, respjson, err = tg.GetUpdates(updatesoffset)
	if err != nil {
		log("tg.GetUpdates: %v", err)
		os.Exit(1)
	}

	var m, prevm tg.Message
	for _, u := range uu {

		log("# UpdateId:%d ", u.UpdateId)

		/*
			if len(TgUpdateLog) > 0 && u.UpdateId < TgUpdateLog[len(TgUpdateLog)-1] {
				log("WARNING this telegram update id:%d is older than last id:%d, skipping", u.UpdateId, TgUpdateLog[len(TgUpdateLog)-1])
				continue
			}
		*/

		if slices.Contains(Config.TgUpdateLog, u.UpdateId) {
			log("WARNING this telegram update id:%d was already processed, skipping", u.UpdateId)
			continue
		}

		Config.TgUpdateLog = append(Config.TgUpdateLog, u.UpdateId)
		if len(Config.TgUpdateLog) > Config.TgUpdateLogMaxSize {
			Config.TgUpdateLog = Config.TgUpdateLog[len(Config.TgUpdateLog)-Config.TgUpdateLogMaxSize:]
		}
		if err := Config.Put(); err != nil {
			log("ERROR Config.Put: %s", err)
		}

		var iseditmessage bool
		var ischannelpost bool
		if u.Message.MessageId != 0 {
			m = u.Message
		} else if u.EditedMessage.MessageId != 0 {
			m = u.EditedMessage
			iseditmessage = true
		} else if u.ChannelPost.MessageId != 0 {
			m = u.ChannelPost
			ischannelpost = true
		} else if u.EditedChannelPost.MessageId != 0 {
			m = u.EditedChannelPost
			ischannelpost = true
			iseditmessage = true
		} else if u.MyChatMemberUpdated.Date != 0 {
			cmu := u.MyChatMemberUpdated
			report := fmt.Sprintf(
				"*MyChatMemberUpdated*"+NL+
					"from:"+NL+
					SPAC+"username: @%s"+NL+
					SPAC+"id: `%d`"+NL+
					"chat:"+NL+
					SPAC+"id: `%d`"+NL+
					SPAC+"username: @%s"+NL+
					SPAC+"type: %s"+NL+
					SPAC+"title: %s"+NL+
					"old member:"+NL+
					SPAC+"username: @%s"+NL+
					SPAC+"id: `%d`"+NL+
					SPAC+"status: %s"+NL+
					"new member:"+NL+
					SPAC+"username: @%s"+NL+
					SPAC+"id: `%d`"+NL+
					SPAC+"status: %s"+NL+
					"",
				cmu.From.Username, cmu.From.Id,
				cmu.Chat.Id, cmu.Chat.Username, cmu.Chat.Type, tg.Esc(cmu.Chat.Title),
				cmu.OldChatMember.User.Username, cmu.OldChatMember.User.Id, cmu.OldChatMember.Status,
				cmu.NewChatMember.User.Username, cmu.NewChatMember.User.Id, cmu.NewChatMember.Status,
			)
			_, err = tg.SendMessage(tg.SendMessageRequest{
				ChatId: strconv.FormatInt(Config.TgZeChatId, 10),
				Text:   tg.Esc(report),
			})
			if err != nil {
				log("tg.SendMessage: %v", err)
			}
		} else {
			log("WARNING unsupported type of update id:%d received:"+NL+"%s", u.UpdateId, respjson)
			_, err = tg.SendMessage(tg.SendMessageRequest{
				ChatId: strconv.FormatInt(Config.TgZeChatId, 10),
				Text:   tg.Esc(fmt.Sprintf("unsupported type of update (id:%d) received:"+NL+"```"+NL+"%s"+NL+"```", u.UpdateId, respjson)),
			})
			if err != nil {
				log("WARNING tg.SendMessage: %v", err)
				continue
			}
			continue
		}

		if m.Chat.Type == "channel" {
			ischannelpost = true
		}

		if ischannelpost {
			add := true
			for _, i := range Config.TgAllChannelsChatIds {
				if m.Chat.Id == i {
					add = false
				}
			}
			if add {
				Config.TgAllChannelsChatIds = append(Config.TgAllChannelsChatIds, m.Chat.Id)
				sort.Slice(Config.TgAllChannelsChatIds, func(i, j int) bool { return Config.TgAllChannelsChatIds[i] < Config.TgAllChannelsChatIds[j] })
				if err := Config.Put(); err != nil {
					log("ERROR Config.Put: %s", err)
				}
			}
		}

		log("telegram message from:`%s` chat:`%s` text:`%s`", m.From.Username, m.Chat.Username, m.Text)
		if m.Text == "" {
			continue
		}

		shouldreport := true
		if m.From.Id == Config.TgZeChatId {
			shouldreport = false
		}
		var chatadmins string
		if aa, err := tg.GetChatAdministrators(m.Chat.Id); err == nil {
			for _, a := range aa {
				chatadmins += fmt.Sprintf("username:@%s id:%d status:%s  ", a.User.Username, a.User.Id, a.Status)
				if a.User.Id == Config.TgZeChatId {
					shouldreport = false
				}
			}
		} else {
			log("tggetChatAdministrators: %v", err)
		}
		if shouldreport && m.MessageId != 0 {
			_, err = tg.SendMessage(tg.SendMessageRequest{
				ChatId: strconv.FormatInt(Config.TgZeChatId, 10),
				Text: fmt.Sprintf(
					"*Message*"+NL+
						"from: username:@%s id:`%d`"+NL+
						"chat: username:@%s id:%d type:%s title:%s"+NL+
						"chat admins: %s"+NL+
						"iseditmessage:%v"+NL+
						"text:"+NL+
						"```"+NL+
						"%s"+NL+
						"```",
					m.From.Username, m.From.Id,
					m.Chat.Id, m.Chat.Username, m.Chat.Type, tg.Esc(m.Chat.Title),
					chatadmins,
					iseditmessage,
					m.Text,
				),
			})
			if err != nil {
				log("tg.SendMessage: %v", err)
				continue
			}
		}

		if strings.TrimSpace(m.Text) == "/id" {
			_, err = tg.SendMessage(tg.SendMessageRequest{
				ChatId:           strconv.FormatInt(m.Chat.Id, 10),
				ReplyToMessageId: m.MessageId,
				Text:             fmt.Sprintf("username `%s`"+NL+"user id `%d`"+NL+"chat id `%d`", m.From.Username, m.From.Id, m.Chat.Id),
			},
			)
			if err != nil {
				log("tg.SendMessage: %v", err)
			}
		}

		if strings.TrimSpace(m.Text) == Config.TgCommandChannels {
			var totalchannels, removedchannels int
			totalchannels = len(Config.TgAllChannelsChatIds)
			for _, i := range Config.TgAllChannelsChatIds {
				var err error
				c, getChatErr := tg.GetChat(i)
				if getChatErr != nil {
					if strings.Contains(getChatErr.Error(), "Bad Request: chat not found") {
						// Remove the channel
						removedchannels += 1
						continue
					}
					_, err = tg.SendMessage(tg.SendMessageRequest{
						ChatId: strconv.FormatInt(m.Chat.Id, 10),
						Text:   tg.Esc(fmt.Sprintf("id:%d err:%v", i, getChatErr)),
					})
					if err != nil {
						log("tg.SendMessage: %v", err)
					}
					continue
				}
				chatinfo := c.Title
				if c.Username != "" {
					chatinfo += " " + fmt.Sprintf("https://t.me/%s", c.Username)
				} else if c.InviteLink != "" {
					chatinfo += " " + c.InviteLink
				}
				_, err = tg.SendMessage(tg.SendMessageRequest{
					ChatId: strconv.FormatInt(m.Chat.Id, 10),
					Text:   tg.Esc(chatinfo),
				})
				if err != nil {
					log("tg.SendMessage: %v", err)
				}
			}
			totalmessage := fmt.Sprintf("Total %d channels.", totalchannels)
			if removedchannels > 0 {
				totalmessage += NL + fmt.Sprintf("Removed %d channels.", removedchannels)
			}
			_, err = tg.SendMessage(tg.SendMessageRequest{
				ChatId:           strconv.FormatInt(m.Chat.Id, 10),
				ReplyToMessageId: m.MessageId,
				Text:             tg.Esc(totalmessage),
			})
			if err != nil {
				log("tg.SendMessage: %v", err)
			}
		}

		if strings.TrimSpace(m.Text) == Config.TgCommandChannelsPromoteAdmin {
			var total, totalok int
			for _, i := range Config.TgAllChannelsChatIds {
				success, err := tg.PromoteChatMember(strconv.FormatInt(i, 10), strconv.FormatInt(m.From.Id, 10))
				total++
				if success != true || err != nil {
					log("tgpromoteChatMember %d %d: %v", i, m.From.Id, err)
				} else {
					totalok++
					log("tgpromoteChatMember %d %d: ok", i, m.From.Id)
				}
			}
			_, err = tg.SendMessage(tg.SendMessageRequest{
				ChatId:           strconv.FormatInt(m.Chat.Id, 10),
				ReplyToMessageId: m.MessageId,
				Text:             tg.Esc(fmt.Sprintf("ok for %d of total %d channels.", totalok, total)),
			})
			if err != nil {
				log("tg.SendMessage: %v", err)
			}
		}

		if strings.TrimSpace(m.Text) == Config.TgQuest1 {
			_, err = tg.SendMessage(tg.SendMessageRequest{
				ChatId: strconv.FormatInt(m.Chat.Id, 10),
				Text:   tg.Code(Config.TgQuest1Key),
			})
			if err != nil {
				log("tg.SendMessage: %v", err)
			}
		}
		if strings.TrimSpace(m.Text) == Config.TgQuest2 {
			_, err = tg.SendMessage(tg.SendMessageRequest{
				ChatId: strconv.FormatInt(m.Chat.Id, 10),
				Text:   tg.Code(Config.TgQuest2Key),
			})
			if err != nil {
				log("tg.SendMessage: %v", err)
			}
		}
		if strings.TrimSpace(m.Text) == Config.TgQuest3 {
			_, err = tg.SendMessage(tg.SendMessageRequest{
				ChatId: strconv.FormatInt(m.Chat.Id, 10),
				Text:   tg.Code(Config.TgQuest3Key),
			})
			if err != nil {
				log("tg.SendMessage: %v", err)
			}
		}

		var downloadvideo bool
		if strings.HasPrefix(strings.ToLower(m.Text), "video ") || strings.HasSuffix(strings.ToLower(m.Text), " video") || strings.ToLower(prevm.Text) == "video" || strings.HasPrefix(strings.ToLower(m.Chat.Title), "vi") {
			downloadvideo = true
		}
		prevm = m

		var videos []YtVideo

		if mm := YtListRe.FindStringSubmatch(m.Text); len(mm) > 1 {
			videos, err = getList(mm[1])
			if err != nil {
				log("getList: %v", err)
				continue
			}
		} else if mm := YtRe.FindStringSubmatch(m.Text); len(mm) > 1 {
			videos = []YtVideo{YtVideo{Id: mm[1]}}
		}

		if len(videos) > 0 {

			var postingerr error
			var vinfo *ytdl.Video
			for _, v := range videos {
				vinfo, err = YtdlCl.GetVideoContext(Ctx, v.Id)
				if err != nil {
					log("ERROR GetVideoContext: %v", err)
					postingerr = err
					break
				}

				if downloadvideo {
					if err := postVideo(v, vinfo, m); err != nil {
						log("ERROR postVideo: %v", err)
						postingerr = err
						break
					}
				} else {
					if err := postAudio(v, vinfo, m); err != nil {
						log("ERROR postAudio: %v", err)
						postingerr = err
						break
					}
				}

				if len(videos) > 3 {
					time.Sleep(11 * time.Second)
				}
			}

			if postingerr == nil {
				if ischannelpost {
					// TODO do not delete if playlist
					if err := tg.DeleteMessage(tg.DeleteMessageRequest{
						ChatId:    strconv.FormatInt(m.Chat.Id, 10),
						MessageId: m.MessageId,
					}); err != nil {
						log("tg.DeleteMessage: %v", err)
					}
				}
			} else {
				if _, err := tg.SendMessage(tg.SendMessageRequest{
					ChatId:           strconv.FormatInt(m.Chat.Id, 10),
					ReplyToMessageId: m.MessageId,
					Text:             tg.Esc(fmt.Sprintf("ERROR %v", postingerr)),
				}); err != nil {
					log("tg.SendMessage: %v", err)
				}
			}

		}
	}

	return
}

func postVideo(v YtVideo, vinfo *ytdl.Video, m tg.Message) error {
	var videoFormat, videoSmallestFormat ytdl.Format

	var tgdeleteMessages []tg.DeleteMessageRequest
	defer func(mm *[]tg.DeleteMessageRequest) {
		for _, dmr := range *mm {
			tg.DeleteMessage(dmr)
		}
	}(&tgdeleteMessages)

	for _, f := range vinfo.Formats.WithAudioChannels() {
		if !strings.Contains(f.MimeType, "/mp4") {
			continue
		}
		fsize := f.ContentLength
		if fsize == 0 {
			fsize = int64(f.Bitrate / 8 * int(vinfo.Duration.Seconds()))
		}
		if !strings.HasPrefix(f.MimeType, "video/mp4") || f.QualityLabel == "" || f.AudioQuality == "" {
			continue
		}
		flang := strings.ToLower(f.LanguageDisplayName())
		log("format: ContentLength:%dmb Language:%#v", f.ContentLength>>20, flang)
		if flang != "" {
			skip := true
			for _, l := range Config.YtDownloadLanguages {
				if strings.Contains(flang, l) {
					skip = false
				}
			}
			if skip {
				continue
			}
		}
		if videoSmallestFormat.ItagNo == 0 || f.Bitrate < videoSmallestFormat.Bitrate {
			videoSmallestFormat = f
		}
		if fsize < Config.TgMaxFileSizeBytes && f.Bitrate > videoFormat.Bitrate {
			videoFormat = f
		}
	}

	var targetVideoBitrateKbps int64
	if videoFormat.ItagNo == 0 {
		videoFormat = videoSmallestFormat
		targetVideoSize := int64(Config.TgMaxFileSizeBytes - (Config.TgAudioBitrateKbps*1024*int64(vinfo.Duration.Seconds()+1))/8)
		targetVideoBitrateKbps = int64(((targetVideoSize * 8) / int64(vinfo.Duration.Seconds()+1)) / 1024)
	}

	ytstream, ytstreamsize, err := YtdlCl.GetStreamContext(Ctx, vinfo, &videoFormat)
	if err != nil {
		return fmt.Errorf("GetStreamContext: %w", err)
	}
	defer ytstream.Close()

	log(
		"downloading youtu.be/%s video size:%dmb quality:%s bitrate:%dkbps duration:%s language:%#v",
		v.Id,
		ytstreamsize>>20,
		videoFormat.QualityLabel,
		videoFormat.Bitrate>>10,
		vinfo.Duration,
		videoFormat.LanguageDisplayName(),
	)

	tgvideoCaption := fmt.Sprintf(
		"%s %s"+NL+
			"youtu.be/%s %s %s ",
		vinfo.Title, vinfo.PublishDate.Format("2006/01/02"),
		v.Id, vinfo.Duration, videoFormat.QualityLabel,
	)
	if v.PlaylistId != "" && v.PlaylistTitle != "" {
		tgvideoCaption += NL + fmt.Sprintf(
			"%d/%d %s ",
			v.PlaylistIndex+1, v.PlaylistSize, v.PlaylistTitle,
		)
	}

	tgvideoFilename := fmt.Sprintf("%s.%s.mp4", ts(), v.Id)
	tgvideoFile, err := os.OpenFile(tgvideoFilename, os.O_RDWR|os.O_CREATE, 0600)
	if err != nil {
		return fmt.Errorf("os.OpenFile: %w", err)
	}

	t0 := time.Now()
	_, err = io.Copy(tgvideoFile, ytstream)
	if err != nil {
		return fmt.Errorf("download youtu.be/%s video: %w", v.Id, err)
	}

	if err := ytstream.Close(); err != nil {
		log("ytstream.Close: %v", err)
	}
	if err := tgvideoFile.Close(); err != nil {
		return fmt.Errorf("os.File.Close: %w", err)
	}

	log("downloaded youtu.be/%s video in %v", v.Id, time.Since(t0).Truncate(time.Second))

	if Config.FfmpegPath != "" && targetVideoBitrateKbps > 0 {
		filename2 := fmt.Sprintf("%s.%s.v%dk.a%dk.mp4", ts(), v.Id, targetVideoBitrateKbps, Config.TgAudioBitrateKbps)
		err := FfmpegTranscode(tgvideoFilename, filename2, targetVideoBitrateKbps, Config.TgAudioBitrateKbps)
		if err != nil {
			return fmt.Errorf("FfmpegTranscode `%s`: %w", tgvideoFilename, err)
		}
		tgvideoCaption += NL + fmt.Sprintf("(transcoded to video:%dkbps audio:%dkbps)", targetVideoBitrateKbps, Config.TgAudioBitrateKbps)
		if err := os.Remove(tgvideoFilename); err != nil {
			log("os.Remove `%s`: %v", tgvideoFilename, err)
		}
		tgvideoFilename = filename2
	}

	tgvideoReader, err := os.Open(tgvideoFilename)
	if err != nil {
		return fmt.Errorf("os.Open: %w", err)
	}
	defer tgvideoReader.Close()

	if _, err := tg.SendVideoFile(tg.SendVideoFileRequest{
		ChatId:   strconv.FormatInt(m.Chat.Id, 10),
		Caption:  tgvideoCaption,
		Video:    tgvideoReader,
		Width:    videoFormat.Width,
		Height:   videoFormat.Height,
		Duration: vinfo.Duration,
	}); err != nil {
		return fmt.Errorf("tgsendVideoFile: %w", err)
	}

	if err := tgvideoReader.Close(); err != nil {
		log("os.File.Close: %v", err)
	}
	if err := os.Remove(tgvideoFilename); err != nil {
		log("os.Remove: %v", err)
	}

	return nil
}

func postAudio(v YtVideo, vinfo *ytdl.Video, m tg.Message) error {
	var audioFormat, audioSmallestFormat ytdl.Format

	var tgdeleteMessages []tg.DeleteMessageRequest
	defer func(mm *[]tg.DeleteMessageRequest) {
		for _, dmr := range *mm {
			tg.DeleteMessage(dmr)
		}
	}(&tgdeleteMessages)

	for _, f := range vinfo.Formats.WithAudioChannels() {
		if !strings.Contains(f.MimeType, "/mp4") {
			continue
		}
		fsize := f.ContentLength
		if fsize == 0 {
			fsize = int64(f.Bitrate / 8 * int(vinfo.Duration.Seconds()))
		}
		if !strings.HasPrefix(f.MimeType, "audio/mp4") {
			continue
		}
		flang := strings.ToLower(f.LanguageDisplayName())
		log("format: ContentLength:%dmb Language:%#v", f.ContentLength>>20, flang)
		if flang != "" {
			skip := true
			for _, l := range Config.YtDownloadLanguages {
				if strings.Contains(flang, l) {
					skip = false
				}
			}
			if skip {
				continue
			}
		}
		if audioSmallestFormat.ItagNo == 0 || f.Bitrate < audioSmallestFormat.Bitrate {
			audioSmallestFormat = f
		}
		if fsize < Config.TgMaxFileSizeBytes && f.Bitrate > audioFormat.Bitrate {
			audioFormat = f
		}
	}

	var targetAudioBitrateKbps int64
	if audioFormat.ItagNo == 0 {
		audioFormat = audioSmallestFormat
		targetAudioBitrateKbps = int64(((Config.TgMaxFileSizeBytes * 8) / int64(vinfo.Duration.Seconds()+1)) / 1024)
	}

	ytstream, ytstreamsize, err := YtdlCl.GetStreamContext(Ctx, vinfo, &audioFormat)
	if err != nil {
		return fmt.Errorf("GetStreamContext: %w", err)
	}
	defer ytstream.Close()

	if ytstreamsize == 0 {
		return fmt.Errorf("GetStreamContext: stream size is zero")
	}

	log(
		"downloading youtu.be/%s audio size:%dmb bitrate:%dkbps duration:%s language:%#v",
		v.Id,
		ytstreamsize>>20,
		audioFormat.Bitrate>>10,
		vinfo.Duration,
		audioFormat.LanguageDisplayName(),
	)

	tgaudioCaption := fmt.Sprintf(
		"%s %s "+NL+
			"youtu.be/%s %s %dkbps ",
		vinfo.Title, vinfo.PublishDate.Format("2006/01/02"),
		v.Id, vinfo.Duration, audioFormat.Bitrate/1024,
	)
	if v.PlaylistId != "" && v.PlaylistTitle != "" {
		tgaudioCaption += NL + fmt.Sprintf(
			"%d/%d %s ",
			v.PlaylistIndex+1, v.PlaylistSize, v.PlaylistTitle,
		)
	}

	tgaudioFilename := fmt.Sprintf("%s.%s.m4a", ts(), v.Id)
	tgaudioFile, err := os.OpenFile(tgaudioFilename, os.O_RDWR|os.O_CREATE, 0600)
	if err != nil {
		return fmt.Errorf("create file: %w", err)
	}

	t0 := time.Now()
	if _, err := io.Copy(tgaudioFile, ytstream); err != nil {
		return fmt.Errorf("download youtu.be/%s audio: %w", v.Id, err)
	}

	if err := ytstream.Close(); err != nil {
		log("ytstream.Close: %v", err)
	}
	if err := tgaudioFile.Close(); err != nil {
		return fmt.Errorf("os.File.Close: %w", err)
	}

	log("downloaded youtu.be/%s audio in %v", v.Id, time.Since(t0).Truncate(time.Second))

	if Config.FfmpegPath != "" && targetAudioBitrateKbps > 0 {
		filename2 := fmt.Sprintf("%s.%s.a%dk.m4a", ts(), v.Id, targetAudioBitrateKbps)
		err := FfmpegTranscode(tgaudioFilename, filename2, 0, targetAudioBitrateKbps)
		if err != nil {
			return fmt.Errorf("FfmpegTranscode `%s`: %w", tgaudioFilename, err)
		}
		tgaudioCaption += NL + fmt.Sprintf("(transcoded to audio:%dkbps)", targetAudioBitrateKbps)
		if err := os.Remove(tgaudioFilename); err != nil {
			log("os.Remove `%s`: %v", tgaudioFilename, err)
		}
		tgaudioFilename = filename2
	}

	tgaudioReader, err := os.Open(tgaudioFilename)
	if err != nil {
		return fmt.Errorf("os.Open: %w", err)
	}
	log("DEBUG tgaudioReader==%#v", tgaudioReader)
	defer tgaudioReader.Close()

	if _, err := tg.SendAudioFile(tg.SendAudioFileRequest{
		ChatId:    strconv.FormatInt(m.Chat.Id, 10),
		Caption:   tgaudioCaption,
		Performer: vinfo.Author,
		Title:     vinfo.Title,
		Duration:  vinfo.Duration,
		Audio:     tgaudioReader,
	}); err != nil {
		return fmt.Errorf("tgsendAudioFile: %w", err)
	}

	if err := tgaudioReader.Close(); err != nil {
		log("os.File.Close: %v", err)
	}
	if err := os.Remove(tgaudioFilename); err != nil {
		log("os.Remove: %v", err)
	}

	return nil
}

func getList(ytlistid string) (ytitems []YtVideo, err error) {
	// https://developers.google.com/youtube/v3/docs/playlists
	var PlaylistUrl = fmt.Sprintf("https://www.googleapis.com/youtube/v3/playlists?maxResults=%d&part=snippet&id=%s&key=%s", Config.YtMaxResults, ytlistid, Config.YtKey)
	var playlists YtPlaylists
	err = getJson(PlaylistUrl, &playlists, nil)
	if err != nil {
		return nil, err
	}

	if len(playlists.Items) < 1 {
		return nil, fmt.Errorf("no playlists found with provided id %s", ytlistid)
	}
	if len(playlists.Items) > 1 {
		return nil, fmt.Errorf("more than one (%d) playlists found with provided id %s", len(playlists.Items), ytlistid)
	}

	log("playlist title: %s", playlists.Items[0].Snippet.Title)

	listtitle := playlists.Items[0].Snippet.Title

	var videos []YtPlaylistItemSnippet
	nextPageToken := ""

	for nextPageToken != "" || len(videos) == 0 {
		// https://developers.google.com/youtube/v3/docs/playlistItems
		var PlaylistItemsUrl = fmt.Sprintf("https://www.googleapis.com/youtube/v3/playlistItems?maxResults=%d&part=snippet&playlistId=%s&key=%s&pageToken=%s", Config.YtMaxResults, ytlistid, Config.YtKey, nextPageToken)

		var playlistItems YtPlaylistItems
		err = getJson(PlaylistItemsUrl, &playlistItems, nil)
		if err != nil {
			return nil, err
		}

		if playlistItems.NextPageToken != nextPageToken {
			nextPageToken = playlistItems.NextPageToken
		} else {
			nextPageToken = ""
		}

		for _, i := range playlistItems.Items {
			videos = append(videos, i.Snippet)
		}
	}

	//sort.Slice(videos, func(i, j int) bool { return videos[i].PublishedAt < videos[j].PublishedAt })

	for _, vid := range videos {
		ytitems = append(
			ytitems,
			YtVideo{
				Id:            vid.ResourceId.VideoId,
				PlaylistId:    vid.PlaylistId,
				PlaylistIndex: vid.Position,
				PlaylistSize:  int64(len(videos)),
				PlaylistTitle: listtitle,
			},
		)
	}

	return ytitems, nil
}

func FfmpegTranscode(filename, filename2 string, videoBitrateKbps, audioBitrateKbps int64) (err error) {
	if videoBitrateKbps > 0 {
		log("transcoding to video:%dkbps audio:%dkbps ", videoBitrateKbps, audioBitrateKbps)
	} else if audioBitrateKbps > 0 {
		log("transcoding to audio:%dkbps", audioBitrateKbps)
	} else {
		return fmt.Errorf("empty both videoBitrateKbps and audioBitrateKbps")
	}

	ffmpegArgs := append(Config.FfmpegGlobalOptions,
		"-i", filename,
		"-f", "mp4",
	)
	if videoBitrateKbps > 0 {
		ffmpegArgs = append(ffmpegArgs,
			"-c:v", "h264",
			"-b:v", fmt.Sprintf("%dk", videoBitrateKbps),
		)
	}
	if audioBitrateKbps > 0 {
		ffmpegArgs = append(ffmpegArgs,
			"-c:a", "aac",
			"-b:a", fmt.Sprintf("%dk", audioBitrateKbps),
		)
	}
	ffmpegArgs = append(ffmpegArgs,
		filename2,
	)

	ffmpegCmd := exec.Command(Config.FfmpegPath, ffmpegArgs...)

	ffmpegCmdStderrPipe, err := ffmpegCmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("ffmpeg StderrPipe: %w", err)
	}

	t0 := time.Now()
	err = ffmpegCmd.Start()
	if err != nil {
		return fmt.Errorf("ffmpeg Start: %w", err)
	}

	log("started command `%s`", ffmpegCmd.String())

	_, err = io.Copy(os.Stderr, ffmpegCmdStderrPipe)
	if err != nil {
		log("copy from ffmpeg stderr: %v", err)
	}

	err = ffmpegCmd.Wait()
	if err != nil {
		return fmt.Errorf("ffmpeg Wait: %w", err)
	}

	log("transcoded in %v", time.Since(t0).Truncate(time.Second))

	return nil
}

func beats(td time.Duration) int {
	return int(td / BEAT)
}

func ts() string {
	tnow := time.Now().In(time.FixedZone("IST", 330*60))
	return fmt.Sprintf(
		"%d%02d%02d:%02d%02d+",
		tnow.Year()%1000, tnow.Month(), tnow.Day(),
		tnow.Hour(), tnow.Minute(),
	)
}

func log(msg string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, ts()+" "+msg+NL, args...)
}

func (config *TgZeConfig) Get() error {
	req, err := http.NewRequest(http.MethodGet, config.YssUrl, nil)
	if err != nil {
		return err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	if resp.StatusCode != 200 {
		return fmt.Errorf("yss response status %s", resp.Status)
	}

	rbb, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	if err := yaml.Unmarshal(rbb, config); err != nil {
		return err
	}

	if config.DEBUG {
		log("DEBUG Config.Get: %+v", config)
	}

	return nil
}

func (config *TgZeConfig) Put() error {
	if config.DEBUG {
		log("DEBUG Config.Put %s %+v", config.YssUrl, config)
	}

	rbb, err := yaml.Marshal(config)
	if err != nil {
		return err
	}

	req, err := http.NewRequest(http.MethodPut, config.YssUrl, bytes.NewBuffer(rbb))
	if err != nil {
		return err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	if resp.StatusCode != 200 {
		return fmt.Errorf("yss response status %s", resp.Status)
	}

	return nil
}

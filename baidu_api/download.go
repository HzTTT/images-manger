package baidu_api

import (
	"baidu_tool/utils"
	"encoding/json"
	"fmt"
	"github.com/sirupsen/logrus"
	"github.com/vbauerster/mpb"
	"github.com/vbauerster/mpb/decor"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type DownloadLinkResp struct {
	Errmsg    string          `json:"errmsg"`
	Errno     int8            `json:"errno"`
	List      []*DownloadInfo `json:"list"`
	RequestID string          `json:"request_id"`
}

type DownloadInfo struct {
	Category int8   `json:"category"`
	DLink    string `json:"dlink"`
	Filename string `json:"filename"`
	FsID     int64  `json:"fs_id"`
	MD5      string `json:"md5"`
	Path     string `json:"path"`
	Size     int64  `json:"size"`
}

type fileIndexPath struct {
	FilePath string
	Index    int
}

const MB50 = 50 * 1024 * 1024

// DownloadFileOrDir 下载文件或者下载文件夹中的文件们
// @author StarkSim
// @param accessToken 身份凭证
// @param sources 文件下载信息
// @param unusedPath 不需要的文件路径前缀，让下载的文件没有太多不需要的前缀
func DownloadFileOrDir(accessToken string, sources []*FileOrDir, unusedPath string) error {
	var fsIDList []int64
	for _, item := range sources {
		// 下载一个文件
		if item.IsDir == 1 {
			continue
		} else {
			// 先收集 fs_id
			fsIDList = append(fsIDList, item.FsId)
		}
	}

	// 用 fs_id 换取下载地址
	downloadInfos, err := getDownloadInfo(accessToken, fsIDList)
	if err != nil {
		return err
	}

	// 拿到下载地址后，开始协程下载
	// 协程下载最高并发，cpu 数量
	maxConcurrentNum := min(runtime.NumCPU(), 16)
	limitChan := make(chan struct{}, maxConcurrentNum)
	defer close(limitChan)
	client := http.Client{}
	// 整理文件结果的协程要有信号量来知道全都处理好了，主协程才能结束
	joinSliceWG := &sync.WaitGroup{}

	// 进度条使用的 wg
	mpbWG := &sync.WaitGroup{}
	progressBars := mpb.New(mpb.WithWaitGroup(mpbWG))
	for _, downloadInfo := range downloadInfos {

		// 如果文件已存在，并且大小正确，那么就跳过
		finalDownloadFilePath := fmt.Sprintf(".%s", strings.TrimPrefix(downloadInfo.Path, unusedPath))
		logrus.Infof(finalDownloadFilePath)
		finalFileInfo, err := os.Stat(finalDownloadFilePath)
		if os.IsNotExist(err) {
			// 不存在，不作为
		} else {
			// 存在
			if finalFileInfo.Size() == downloadInfo.Size {
				// 文件大小 ok，跳过
				continue
			} else {
				// 文件不对，删除重新下
				if err = os.Remove(finalDownloadFilePath); err != nil {
					fmt.Printf("删除还没完全的文件错误: %v\n", err)
					return err
				}
			}
		}
		// 如果文件切片，每个 50 MB
		sliceNum := int(downloadInfo.Size / MB50)

		// 每当要下载一个完整的文件，就加一条进度条
		mpbWG.Add(1)
		tempBar := progressBars.AddBar(
			downloadInfo.Size,
			mpb.PrependDecorators(
				decor.Name(downloadInfo.Filename),
				decor.Percentage(decor.WCSyncSpace),
			),
			mpb.AppendDecorators(
				decor.OnComplete(
					decor.EwmaETA(decor.ET_STYLE_GO, 30, decor.WCSyncWidth),
					"done",
				),
			),
			mpb.BarRemoveOnComplete(),
		)

		// 准备下载请求
		_url := downloadInfo.DLink + "&access_token=" + accessToken
		realUrl, err := url.Parse(_url)
		if err != nil {
			return err
		}
		// 请先看非协程部分代码，只有 limitChan 会起到代码阻塞作用，其他的下载，结果拼接过程都是在协程中进行的。
		// 目的是为了充分发挥网络并发能力，可以让多个文件同时以切片形式下载

		// 每一个分片下载的文件都有一个信道作为最后收集碎片文件信息的媒介
		tempFileChan := make(chan *fileIndexPath, 5)
		// 有一个独立协程做收集文件信息并最后拼接操作
		joinSliceWG.Add(1)
		go func(innerFileChan chan *fileIndexPath, finalFileName string, barWG *sync.WaitGroup) {
			var sliceFileIndexPaths []*fileIndexPath
			for tempFileIndexPath := range innerFileChan {
				sliceFileIndexPaths = append(sliceFileIndexPaths, tempFileIndexPath)
			}
			targetFile, err := os.OpenFile("."+strings.TrimPrefix(finalFileName, unusedPath), os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0777)
			defer targetFile.Close()
			if err != nil {
				logrus.Errorf("打开目标文件错误: %v\n", err)
				return
			}
			// 按升序排序
			sort.Slice(sliceFileIndexPaths, func(i, j int) bool {
				return sliceFileIndexPaths[i].Index < sliceFileIndexPaths[j].Index
			})

			// 拼接后要删除文件，都删除完拼接过程再算结束
			removeSliceWG := &sync.WaitGroup{}
			for i := 0; i < len(sliceFileIndexPaths); i++ {
				sliceFile, err := os.Open(sliceFileIndexPaths[i].FilePath)
				if err != nil {
					logrus.Errorf("打开碎片文件错误: %v\n", err)
					return
				}
				_, err = io.CopyBuffer(targetFile, sliceFile, make([]byte, MB50))
				if err != nil {
					logrus.Errorf("拼接文件错误: %v\n", err)
					return
				}
				removeSliceWG.Add(1)
				go func(sliceFile string, wg *sync.WaitGroup) {
					if err = os.Remove(sliceFile); err != nil {
						logrus.Errorf("删除碎片文件错误: %v\n", err)
						return
					}
					wg.Done()
				}(sliceFileIndexPaths[i].FilePath, removeSliceWG)
			}
			logrus.Infof("文件拼接完成: %s\n", finalFileName)
			// 进度条展示完成
			barWG.Done()

			// 等待删除结束
			removeSliceWG.Wait()

			// 文件拼接完成，意味着单元程序可以结束
			joinSliceWG.Done()

		}(tempFileChan, downloadInfo.Path, mpbWG)

		// 分片下载需要一个信号量让接受文件结果协程知道收集可以结束
		downloadWG := &sync.WaitGroup{}
		// 分片下载
		for i := 0; i <= sliceNum; i++ {
			logrus.Info("下载文件slice: ", i)
			_i := i
			downloadWG.Add(1)
			// 先得到最终的碎片文件路径
			localDownloadFilePath := fmt.Sprintf(".%s_%d", strings.TrimPrefix(downloadInfo.Path, unusedPath), i)
			// 如果碎片文件已存在，那么直接算作完成跳过
			limitChan <- struct{}{}
			downloadSlideFile(realUrl, _i, localDownloadFilePath, client, tempFileChan, tempBar, downloadWG)
			<-limitChan
		}
		// 这一步也不阻塞，因为还有下一个文件
		go func(innerDownloadWG *sync.WaitGroup, innerChan chan *fileIndexPath) {
			// 都下载并传输结果完毕
			innerDownloadWG.Wait()
			// 那么就可以关闭结果传输信道，让结果收集者知道传输完了
			close(innerChan)
		}(downloadWG, tempFileChan)
	}
	// 等待进度条都结束
	progressBars.Wait()
	// 这个 wg 结束了，那就都结束了
	joinSliceWG.Wait()
	return nil
}

// 一次性拿到要下载的文件的下载地址们
func getDownloadInfo(accessToken string, fsIDList []int64) ([]*DownloadInfo, error) {
	if fsIDList == nil || len(fsIDList) == 0 {
		return nil, nil
	}
	preUrl := "http://pan.baidu.com/rest/2.0/xpan/multimedia?method=filemetas&access_token=%s&fsids=%s&dlink=1"
	var strFsIDList []string
	for _, fsID := range fsIDList {
		strFsIDList = append(strFsIDList, strconv.FormatInt(fsID, 10))
	}
	fsids := "[" + strings.Join(strFsIDList, ",") + "]"
	resp, err := http.Get(fmt.Sprintf(preUrl, accessToken, fsids))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var downloadResp DownloadLinkResp
	if err = json.Unmarshal(respBytes, &downloadResp); err != nil {
		return nil, err
	}
	if downloadResp.Errno != 0 {
		return nil, fmt.Errorf("api no return or return err: %v", downloadResp)
	}
	return downloadResp.List, nil
}

// 下载文件切片，每个切片 50 MB，rangeEnd 为 -1 时表示下载到文件末尾
func getSlideFile(url *url.URL, rangeStart int, rangeEnd int, fileDownloadPath string, client http.Client) {
	header := http.Header{}
	header.Set("User-Agent", "pan.baidu.com")
	if rangeEnd == -1 {
		header.Set("Range", fmt.Sprintf("bytes=%v-", rangeStart))
	} else {
		header.Set("Range", fmt.Sprintf("bytes=%v-%v", rangeStart, rangeEnd))
	}
	request := http.Request{
		Method: "GET",
		URL:    url,
		Header: header,
	}
	// 重传直到完成
	for {
		resp, err := client.Do(&request)
		if err != nil {
			logrus.Warningf("网络连接错误 clientDo: %v\n", err)
			time.Sleep(time.Second + time.Millisecond*time.Duration(rand.Intn(100)))
			continue
		}
		if resp.StatusCode != 206 && resp.StatusCode != 200 {
			//bts, _ := io.ReadAll(resp.Body)
			//logrus.Warningf("状态码非 206 :%s\n", bts)
			time.Sleep(time.Second + time.Millisecond*time.Duration(rand.Intn(100)))
			continue
		}
		defer resp.Body.Close()
		respBytes, err := io.ReadAll(resp.Body)
		if err != nil {
			//logrus.Errorf("返回读取错误 ioReadAll: %v\n", err)
			continue
		}
		dir, _, err := utils.DivideDirAndFile(fileDownloadPath)
		if err != nil {
			logrus.Errorf("分割文件路径错误: %v\n", err)
			continue
		}
		if err = os.MkdirAll(dir, 0750); err != nil {
			logrus.Errorf("创建文件夹错误 mkdirAll: %v\n", err)
			continue
		}
		if err = os.WriteFile(fileDownloadPath, respBytes, 0666); err != nil {
			logrus.Errorf("写文件错误 osWriteFile: %v\n", err)
			continue
		}
	}
}

func downloadSlideFile(url *url.URL, sliceIndex int, fileDownloadPath string, client http.Client, tempFileChan chan *fileIndexPath, tempBar *mpb.Bar, downloadWG *sync.WaitGroup) {
	_, err := os.Stat(fileDownloadPath)
	if os.IsNotExist(err) {
		// 不存在，准备启动协程下载
		// 要启动下载协程时在获取一个下载进程限制器量
		// 并留点间隔不然百度容易拒绝请求
		time.Sleep(time.Second + time.Millisecond*time.Duration(rand.Intn(100)))
		go func() {
			getSlideFile(url, sliceIndex*MB50, sliceIndex*MB50+MB50-1, fileDownloadPath, client)
			// 保存好文件后，推送自己完成的文件信息
			tempFileChan <- &fileIndexPath{
				FilePath: fileDownloadPath,
				Index:    sliceIndex,
			}
			// 进度条增长
			tempBar.IncrBy(MB50)
			// 下载同步量完成一个
			downloadWG.Done()
		}()
	} else {
		// 存在，成功跳过
		// 执行文件成功下载保存后的步骤，推送自己完成的文件信息
		tempFileChan <- &fileIndexPath{
			FilePath: fileDownloadPath,
			Index:    sliceIndex,
		}
		// 进度条增长
		tempBar.IncrBy(MB50)
		downloadWG.Done()
	}
}

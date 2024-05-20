package client

import (
	"baidu_tool/baidu_api"
	"baidu_tool/utils"
	"fmt"
)

var AppType = make(map[string]*Application)

type Application struct {
	ImageName    string
	DownloadPath string
}

func NewApplication(name string, path string) *Application {
	res := Application{
		ImageName:    name,
		DownloadPath: path,
	}
	AppType[name] = &res
	return &res
}

func (app *Application) DownloadImage(path string, accessToken string) error {
	parentDir, file, err := utils.DivideDirAndFile(app.DownloadPath)
	if err != nil {
		return fmt.Errorf("error dividing dir and file: %w", err)
	}
	dirListResp, err := baidu_api.GetDirByList(accessToken, parentDir)
	if err != nil {
		return fmt.Errorf("error getting dir by list: %w", err)
	}
	// 找到 list 里的 file，只下载这个 file
	foundFile := false
	for _, item := range dirListResp.List {
		if item.ServerFilename == file {
			// 直接下载这个文件，不需要前面的目录
			err = baidu_api.DownloadFileOrDir(accessToken, []*baidu_api.FileOrDir{item}, parentDir)
			if err != nil {
				return fmt.Errorf("error downloading file: %w", err)
			}
			foundFile = true
			break
		}
	}
	if !foundFile {
		return fmt.Errorf("file not found")
	}
	return nil
}

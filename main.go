package main

import (
	"baidu_tool/baidu_api"
	"flag"
	"fmt"
)

func main() {
	var input struct {
		AccessToken string
		Path        string
	}
	flag.StringVar(&input.AccessToken, "access_token", "", "token")
	flag.StringVar(&input.Path, "path", "", "文件或文件夹路径")
	flag.Parse()
	if input.AccessToken == "" {
		fmt.Printf("input access_token by --access_token [your access token]\n")
	}
	if input.Path == "" {
		fmt.Printf("input file/dir path by --path [file/dir path]\n")
	}

	// 开始搜索，找文件信息
	dirResp, err := baidu_api.GetFileOrDirResp(input.AccessToken, input.Path)
	if err != nil {
		return
	}
	// 如果文件夹信息中没有内容，那么要么是文件，要么是没有
	if dirResp.List == nil || len(dirResp.List) == 0 {
		// 退回上一层路径，再次搜索
		parentDir, file, err := baidu_api.DivideDirAndFile(input.Path)
		if err != nil {
			return
		}
		dirResp, err = baidu_api.GetFileOrDirResp(input.AccessToken, parentDir)
		if err != nil {
			return
		}
		// 看看这次 list 中有没有 file
		if dirResp.List == nil || len(dirResp.List) == 0 {
			fmt.Printf("not found %s\n", input.Path)
			return
		} else {
			// 找到 list 里的 file，只下载这个 file
			foundFile := false
			for _, item := range dirResp.List {
				if item.ServerFilename == file {
					err = baidu_api.DownloadFileOrDir(input.AccessToken, []*baidu_api.FileOrDir{item})
					if err != nil {
						return
					}
					foundFile = true
					break
				}
			}
			if !foundFile {
				fmt.Printf("not found %s, but found %s\n", file, parentDir)
				return
			}
		}
	} else {
		// 找到了，那么这是个文件夹，下载该文件夹和其内部所有文件
		err = baidu_api.DownloadFileOrDir(input.AccessToken, dirResp.List)
		if err != nil {
			return
		}
	}
}

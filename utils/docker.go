package utils

import (
	"context"
	"fmt"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/client"
)

func init() {
	var err error
	DockerClient, err = client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		panic(err)
	}
}

var DockerClient *client.Client

func GetImageList() {
	images, err := DockerClient.ImageList(context.Background(), image.ListOptions{All: true})
	if err != nil {
		panic(err)
	}

	// 输出镜像列表
	for _, i := range images {
		if len(i.RepoTags) > 0 {
			for _, tag := range i.RepoTags {
				fmt.Println("Image Name: ", tag)
			}
		} else {
			fmt.Println("Image ID: ", i.ID, " has no tags")
		}
	}
}

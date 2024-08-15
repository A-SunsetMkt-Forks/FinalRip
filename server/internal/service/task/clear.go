package task

import (
	"sync"

	"github.com/TensoRaws/FinalRip/common/db"
	"github.com/TensoRaws/FinalRip/module/log"
	"github.com/TensoRaws/FinalRip/module/oss"
	"github.com/TensoRaws/FinalRip/module/queue"
	"github.com/TensoRaws/FinalRip/module/resp"
	"github.com/gin-gonic/gin"
)

type ClearRequest struct {
	VideoKey string `form:"video_key" binding:"required"`
}

// Clear 清理任务 (POST /new)
func Clear(c *gin.Context) {
	// 绑定参数
	var req NewRequest
	if err := c.ShouldBind(&req); err != nil {
		resp.AbortWithMsg(c, err.Error())
		return
	}

	// 检查任务是否存在
	if !db.CheckTaskExist(req.VideoKey) {
		resp.AbortWithMsg(c, "Task not found, please check the video key")
		return
	}

	clips, err := db.GetVideoClips(req.VideoKey)
	if err != nil {
		resp.AbortWithMsg(c, err.Error())
		return
	}

	// 检查 Cut 任务是否处理完成
	if len(clips) == 0 {
		resp.AbortWithMsg(c, "Please wait for the cut task to complete before clearing the task")
		return
	}

	// 清理 OSS
	ossDelObjKeys := make([]string, 0)
	task, _ := db.GetTask(req.VideoKey)
	ossDelObjKeys = append(ossDelObjKeys, task.Key)
	ossDelObjKeys = append(ossDelObjKeys, task.EncodeKey)

	for _, clip := range clips {
		if clip.ClipKey != "" {
			ossDelObjKeys = append(ossDelObjKeys, clip.ClipKey)
		}
		if clip.EncodeKey != "" {
			ossDelObjKeys = append(ossDelObjKeys, clip.EncodeKey)
		}
	}

	ossDelMulti(ossDelObjKeys)
	log.Logger.Infof("Deleted files from OSS: %v", ossDelObjKeys)

	// 清理数据库
	err = db.DeleteTask(req.VideoKey)
	if err != nil {
		resp.AbortWithMsg(c, err.Error())
		return
	}

	err = db.DeleteVideoClips(req.VideoKey)
	if err != nil {
		resp.AbortWithMsg(c, err.Error())
		return
	}
	log.Logger.Infof("Deleted task from database: %s", req.VideoKey)

	// 一定要在删除数据库记录和 OSS 之后再取消任务，否则会导致 Merge 任务无法正常取消，以及 Encode 任务异常下载 OSS
	// 检查任务是否处理完成
	if !db.CheckTaskComplete(req.VideoKey) {
		// 清理任务队列，倒序删除
		for i := len(clips) - 1; i >= 0; i-- {
			clip := clips[i]
			err = queue.Isp.CancelProcessing(clip.TaskID)
			if err != nil {
				log.Logger.Errorf("Failed to cancel processing task: %s", err)
			}
			err := queue.Isp.DeleteTask(queue.ENCODE_QUEUE, clip.TaskID)
			if err != nil {
				log.Logger.Errorf("Failed to delete task from encode queue: %s", err)
			}
		}
	}

	resp.OK(c)
}

func ossDelMulti(keys []string) {
	var wg sync.WaitGroup
	for _, key := range keys {
		wg.Add(1)
		go func(k string) {
			err := oss.Delete(k)
			if err != nil {
				log.Logger.Errorf("Failed to delete file from OSS: %s", err)
			}
			wg.Done()
		}(key)
	}
	wg.Wait()
}
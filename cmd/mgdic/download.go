package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"mgdic/internal/version"
)

// 首次运行时如果既没有词典库、也没有源 CSV，就从上游自动下载。
//
// 这样发布出去只要一个 exe：双击 → 下载数据（66MB）→ 自动建库 → 能用。
// 下载地址指向 ECDICT 官方仓库，我们不镜像、不再分发（版权归上游作者）。
const ecdictURL = "https://raw.githubusercontent.com/skywind3000/ECDICT/master/ecdict.csv"

// downloadFile 把 url 下到 dst，带进度输出。
// 先写 .part，成功后改名，中途断掉不会留下半个坏文件。
func downloadFile(url, dst string) error {
	client := &http.Client{Timeout: 30 * time.Minute}
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "mgdic/"+version.Version)

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("连不上 %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("下载失败，HTTP %d", resp.StatusCode)
	}

	tmp := dst + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	defer func() {
		f.Close()
		os.Remove(tmp) // 成功时已被改名，这里是失败路径的清理
	}()

	total := resp.ContentLength
	var done int64
	buf := make([]byte, 256<<10)
	lastPct := -1
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := f.Write(buf[:n]); werr != nil {
				return werr
			}
			done += int64(n)
			if total > 0 {
				pct := int(done * 100 / total)
				if pct/5 != lastPct/5 { // 每 5% 报一次
					fmt.Printf("\r  下载中 %d%%（%.0f / %.0f MB）", pct,
						float64(done)/(1<<20), float64(total)/(1<<20))
					lastPct = pct
				}
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return rerr
		}
	}
	fmt.Println()
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		return err
	}
	if fi, err := os.Stat(dst); err == nil {
		fmt.Printf("  下载完成：%s（%.0f MB）\n", filepath.Base(dst), float64(fi.Size())/(1<<20))
	}
	return nil
}

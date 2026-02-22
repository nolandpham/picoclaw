package statesync

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/logger"
)

const (
	minSyncIntervalMinutes = 1
)

type Service struct {
	cfg       *config.Config
	workspace string
	interval  time.Duration
	homeDir   string

	client      *minio.Client
	bucket      string
	prefix      string
	mode        string
	manifestKey string

	mu       sync.Mutex
	stopChan chan struct{}
	running  bool
}

type manifest struct {
	Version   int                      `json:"version"`
	UpdatedAt int64                    `json:"updated_at"`
	Files     map[string]manifestEntry `json:"files"`
}

type manifestEntry struct {
	Sha256    string `json:"sha256"`
	Size      int64  `json:"size"`
	UpdatedAt int64  `json:"updated_at"`
}

type localFile struct {
	Path      string
	RelKey    string
	Sha256    string
	Size      int64
	UpdatedAt int64
}

func NewService(cfg *config.Config, workspace string) (*Service, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config is nil")
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("resolve home dir: %w", err)
	}

	r2cfg := cfg.Sync.R2
	endpoint := fmt.Sprintf("%s.r2.cloudflarestorage.com", r2cfg.AccountID)
	client, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(r2cfg.AccessKeyID, r2cfg.SecretAccessKey, ""),
		Secure: true,
		Region: "auto",
	})
	if err != nil {
		return nil, fmt.Errorf("create r2 client: %w", err)
	}

	prefix := strings.Trim(strings.TrimSpace(r2cfg.Prefix), "/")
	if prefix == "" {
		prefix = "picoclaw-state"
	}

	intervalMinutes := cfg.Sync.IntervalMinutes
	if intervalMinutes < minSyncIntervalMinutes {
		intervalMinutes = 5
	}

	mode := strings.TrimSpace(strings.ToLower(cfg.Sync.Mode))
	if mode == "" {
		mode = "r2-first"
	}

	return &Service{
		cfg:         cfg,
		workspace:   workspace,
		homeDir:     home,
		interval:    time.Duration(intervalMinutes) * time.Minute,
		client:      client,
		bucket:      r2cfg.Bucket,
		prefix:      prefix,
		mode:        mode,
		manifestKey: pathJoin(prefix, "manifest.json"),
	}, nil
}

func (s *Service) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.running {
		return nil
	}

	s.stopChan = make(chan struct{})
	s.running = true

	go s.runLoop(s.stopChan)
	logger.InfoCF("sync", "R2 state sync started", map[string]interface{}{
		"interval_minutes": s.interval.Minutes(),
		"mode":             s.mode,
		"bucket":           s.bucket,
		"prefix":           s.prefix,
	})

	return nil
}

func (s *Service) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.running {
		return
	}

	s.running = false
	if s.stopChan != nil {
		close(s.stopChan)
		s.stopChan = nil
	}
}

func (s *Service) runLoop(stopChan chan struct{}) {
	// Initial sync shortly after startup.
	time.AfterFunc(2*time.Second, func() {
		if err := s.syncOnce(context.Background()); err != nil {
			logger.ErrorCF("sync", "Initial sync failed", map[string]interface{}{"error": err.Error()})
		}
	})

	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	for {
		select {
		case <-stopChan:
			return
		case <-ticker.C:
			if err := s.syncOnce(context.Background()); err != nil {
				logger.ErrorCF("sync", "Periodic sync failed", map[string]interface{}{"error": err.Error()})
			}
		}
	}
}

func (s *Service) syncOnce(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	localFiles, err := s.collectLocalFiles()
	if err != nil {
		return err
	}

	remoteManifest, err := s.loadManifest(ctx)
	if err != nil {
		return err
	}

	localIndex := make(map[string]localFile, len(localFiles))
	for _, file := range localFiles {
		localIndex[file.RelKey] = file
	}

	allKeysMap := make(map[string]struct{}, len(remoteManifest.Files)+len(localIndex))
	for key := range localIndex {
		allKeysMap[key] = struct{}{}
	}
	for key := range remoteManifest.Files {
		allKeysMap[key] = struct{}{}
	}

	allKeys := make([]string, 0, len(allKeysMap))
	for key := range allKeysMap {
		allKeys = append(allKeys, key)
	}
	sort.Strings(allKeys)

	changedManifest := false

	for _, key := range allKeys {
		local, hasLocal := localIndex[key]
		remote, hasRemote := remoteManifest.Files[key]

		if hasRemote && (!hasLocal || s.shouldPullRemote(remote, local)) {
			if err := s.pullFile(ctx, key, local.Path); err != nil {
				logger.ErrorCF("sync", "Failed pulling remote file", map[string]interface{}{"key": key, "error": err.Error()})
				continue
			}
			changedManifest = true
			continue
		}

		if hasLocal && (!hasRemote || s.shouldPushLocal(local, remote)) {
			if err := s.pushFile(ctx, local); err != nil {
				logger.ErrorCF("sync", "Failed pushing local file", map[string]interface{}{"key": key, "error": err.Error()})
				continue
			}
			remoteManifest.Files[key] = manifestEntry{
				Sha256:    local.Sha256,
				Size:      local.Size,
				UpdatedAt: local.UpdatedAt,
			}
			changedManifest = true
		}
	}

	if changedManifest {
		remoteManifest.UpdatedAt = time.Now().Unix()
		if err := s.saveManifest(ctx, remoteManifest); err != nil {
			return err
		}
	}

	logger.InfoCF("sync", "R2 sync completed", map[string]interface{}{
		"files_scanned": len(localFiles),
		"manifest_size": len(remoteManifest.Files),
	})
	return nil
}

func (s *Service) shouldPullRemote(remote manifestEntry, local localFile) bool {
	if local.Path == "" {
		return true
	}
	if remote.Sha256 == local.Sha256 {
		return false
	}
	if s.mode == "r2-first" {
		return remote.UpdatedAt >= local.UpdatedAt
	}
	return remote.UpdatedAt > local.UpdatedAt
}

func (s *Service) shouldPushLocal(local localFile, remote manifestEntry) bool {
	if remote.Sha256 == local.Sha256 {
		return false
	}
	if s.mode == "r2-first" {
		return local.UpdatedAt > remote.UpdatedAt
	}
	return local.UpdatedAt >= remote.UpdatedAt
}

func (s *Service) loadManifest(ctx context.Context) (*manifest, error) {
	m := &manifest{Version: 1, Files: map[string]manifestEntry{}}

	obj, err := s.client.GetObject(ctx, s.bucket, s.manifestKey, minio.GetObjectOptions{})
	if err != nil {
		return nil, fmt.Errorf("get manifest object: %w", err)
	}
	defer obj.Close()

	data, err := io.ReadAll(obj)
	if err != nil {
		resp := minio.ToErrorResponse(err)
		if resp.Code == "NoSuchKey" || resp.Code == "NoSuchBucket" {
			return m, nil
		}
		if strings.Contains(strings.ToLower(err.Error()), "not exist") || strings.Contains(strings.ToLower(err.Error()), "no such") {
			return m, nil
		}
		return nil, fmt.Errorf("read manifest: %w", err)
	}

	if len(data) == 0 {
		return m, nil
	}

	if err := json.Unmarshal(data, m); err != nil {
		return nil, fmt.Errorf("decode manifest: %w", err)
	}
	if m.Files == nil {
		m.Files = map[string]manifestEntry{}
	}
	return m, nil
}

func (s *Service) saveManifest(ctx context.Context, m *manifest) error {
	payload, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("encode manifest: %w", err)
	}

	_, err = s.client.PutObject(ctx, s.bucket, s.manifestKey, bytes.NewReader(payload), int64(len(payload)), minio.PutObjectOptions{
		ContentType: "application/json",
	})
	if err != nil {
		return fmt.Errorf("put manifest: %w", err)
	}
	return nil
}

func (s *Service) objectKey(relKey string) string {
	return pathJoin(s.prefix, "files", relKey)
}

func (s *Service) pullFile(ctx context.Context, relKey string, fallbackPath string) error {
	obj, err := s.client.GetObject(ctx, s.bucket, s.objectKey(relKey), minio.GetObjectOptions{})
	if err != nil {
		return err
	}
	defer obj.Close()

	data, err := io.ReadAll(obj)
	if err != nil {
		return err
	}

	localPath := fallbackPath
	if localPath == "" {
		localPath = s.resolveLocalPath(relKey)
	}
	if localPath == "" {
		return fmt.Errorf("cannot resolve local path for key %s", relKey)
	}
	return writeFileAtomic(localPath, data, 0600)
}

func (s *Service) pushFile(ctx context.Context, local localFile) error {
	file, err := os.Open(local.Path)
	if err != nil {
		return err
	}
	defer file.Close()

	_, err = s.client.PutObject(ctx, s.bucket, s.objectKey(local.RelKey), file, local.Size, minio.PutObjectOptions{
		ContentType: "application/octet-stream",
	})
	return err
}

func (s *Service) collectLocalFiles() ([]localFile, error) {
	roots := s.syncRoots()
	files := make([]localFile, 0, 256)

	for _, root := range roots {
		fi, err := os.Stat(root.localPath)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}

		if fi.IsDir() {
			err = filepath.WalkDir(root.localPath, func(path string, d os.DirEntry, walkErr error) error {
				if walkErr != nil {
					return walkErr
				}
				if d.IsDir() {
					return nil
				}
				if strings.HasSuffix(path, ".tmp") {
					return nil
				}
				rel, err := filepath.Rel(root.localPath, path)
				if err != nil {
					return err
				}
				entry, err := toLocalFile(path, pathJoin(root.keyPrefix, filepath.ToSlash(rel)))
				if err != nil {
					return err
				}
				files = append(files, entry)
				return nil
			})
			if err != nil {
				return nil, err
			}
			continue
		}

		entry, err := toLocalFile(root.localPath, root.keyPrefix)
		if err != nil {
			return nil, err
		}
		files = append(files, entry)
	}

	return files, nil
}

type syncRoot struct {
	localPath string
	keyPrefix string
}

func (s *Service) syncRoots() []syncRoot {
	roots := []syncRoot{}
	r2cfg := s.cfg.Sync.R2

	if r2cfg.SyncWorkspace {
		roots = append(roots,
			syncRoot{localPath: filepath.Join(s.workspace, "state"), keyPrefix: "workspace/state"},
			syncRoot{localPath: filepath.Join(s.workspace, "cron"), keyPrefix: "workspace/cron"},
			syncRoot{localPath: filepath.Join(s.workspace, "HEARTBEAT.md"), keyPrefix: "workspace/HEARTBEAT.md"},
			syncRoot{localPath: filepath.Join(s.workspace, "heartbeat.log"), keyPrefix: "workspace/heartbeat.log"},
		)
	}
	if r2cfg.SyncSessions {
		roots = append(roots, syncRoot{localPath: filepath.Join(s.workspace, "sessions"), keyPrefix: "workspace/sessions"})
	}
	if r2cfg.SyncMemory {
		roots = append(roots, syncRoot{localPath: filepath.Join(s.workspace, "memory"), keyPrefix: "workspace/memory"})
	}
	if r2cfg.SyncSkills {
		roots = append(roots, syncRoot{localPath: filepath.Join(s.workspace, "skills"), keyPrefix: "workspace/skills"})
	}
	if r2cfg.SyncConfig {
		roots = append(roots, syncRoot{localPath: filepath.Join(s.homeDir, ".picoclaw", "config.json"), keyPrefix: "home/config.json"})
	}
	if r2cfg.SyncAuth {
		roots = append(roots, syncRoot{localPath: filepath.Join(s.homeDir, ".picoclaw", "auth.json"), keyPrefix: "home/auth.json"})
	}

	return roots
}

func (s *Service) resolveLocalPath(relKey string) string {
	relKey = strings.Trim(relKey, "/")
	if strings.HasPrefix(relKey, "workspace/") {
		rel := strings.TrimPrefix(relKey, "workspace/")
		return filepath.Join(s.workspace, filepath.FromSlash(rel))
	}
	if strings.HasPrefix(relKey, "home/") {
		rel := strings.TrimPrefix(relKey, "home/")
		if rel == "config.json" {
			return filepath.Join(s.homeDir, ".picoclaw", "config.json")
		}
		if rel == "auth.json" {
			return filepath.Join(s.homeDir, ".picoclaw", "auth.json")
		}
	}
	return ""
}

func toLocalFile(path string, relKey string) (localFile, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return localFile{}, err
	}

	h, err := fileSHA256(path)
	if err != nil {
		return localFile{}, err
	}

	return localFile{
		Path:      path,
		RelKey:    strings.Trim(relKey, "/"),
		Sha256:    h,
		Size:      fi.Size(),
		UpdatedAt: fi.ModTime().Unix(),
	}, nil
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	tmp, err := os.CreateTemp(dir, "sync-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}

	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	cleanup = false
	return nil
}

func pathJoin(parts ...string) string {
	clean := make([]string, 0, len(parts))
	for _, p := range parts {
		if p == "" {
			continue
		}
		clean = append(clean, strings.Trim(p, "/"))
	}
	return strings.Join(clean, "/")
}

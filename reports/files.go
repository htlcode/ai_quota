package reports

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type usageFileParser func(context.Context, string, time.Time) ([]DailyModelUsage, error)

type usageFileSource struct {
	Name            string
	Root            string
	SkipDirectories map[string]struct{}
	Parse           usageFileParser
}

func syncUsageFiles(ctx context.Context, cache *usageCache, startTime time.Time, source usageFileSource) []string {
	warnings := make([]string, 0)
	currentPaths := make(map[string]struct{})
	walkFile := func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		isDirectory := entry.IsDir()
		if isDirectory {
			name := entry.Name()
			_, shouldSkip := source.SkipDirectories[name]
			if shouldSkip {
				return filepath.SkipDir
			}
			return nil
		}
		entryName := entry.Name()
		extension := filepath.Ext(entryName)
		isJSONL := strings.EqualFold(extension, ".jsonl")
		if !isJSONL {
			return nil
		}
		fileInfo, infoError := entry.Info()
		if infoError != nil {
			return infoError
		}
		modifiedTime := fileInfo.ModTime()
		isOldFile := modifiedTime.Before(startTime)
		if isOldFile {
			return nil
		}
		currentPaths[path] = struct{}{}
		modifiedNS := modifiedTime.UnixNano()
		fileSize := fileInfo.Size()
		isCurrent, currentError := cache.fileIsCurrent(ctx, source.Name, path, fileSize, modifiedNS)
		if currentError != nil {
			return currentError
		}
		if isCurrent {
			return nil
		}
		rows, parseError := source.Parse(ctx, path, startTime)
		if parseError != nil {
			fileName := filepath.Base(path)
			warning := source.Name + ": " + fileName + " could not be parsed"
			warnings = append(warnings, warning)
			return nil
		}
		replaceError := cache.replaceFile(ctx, source.Name, path, fileSize, modifiedNS, rows)
		return replaceError
	}
	walkError := filepath.WalkDir(source.Root, walkFile)
	if walkError != nil {
		errorMessage := walkError.Error()
		warning := source.Name + ": " + errorMessage
		warnings = append(warnings, warning)
		return warnings
	}
	pruneError := cache.pruneSourceFiles(ctx, source.Name, currentPaths)
	if pruneError != nil {
		errorMessage := pruneError.Error()
		warning := source.Name + ": " + errorMessage
		warnings = append(warnings, warning)
	}
	return warnings
}

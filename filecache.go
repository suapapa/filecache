package filecache

import (
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"strconv"
	"time"
)

// File size constants for use with FileCache.MaxSize.
// For example, cache.MaxSize = 64 * Megabyte
const (
	Kilobyte = 1024
	Megabyte = 1024 * 1024
	Gigabyte = 1024 * 1024 * 1024
)

var (
	DefaultExpireItem int   = 300 // 5 minutes
	DefaultMaxSize    int64 = 32 * Megabyte
	DefaultMaxItems   int   = 32
	DefaultEvery      int   = 60 // 1 minute
)

var (
	InvalidCacheItem = errors.New("invalid cache item")
	ItemIsDirectory  = errors.New("can't cache a directory")
	ItemNotInCache   = errors.New("item not in cache")
	ItemTooLarge     = errors.New("item too large for cache")
	WriteIncomplete  = errors.New("incomplete write of cache item")
)

// Mumber of items to buffer adding to the file cache.
var NewCachePipeSize = 4

// FileCache represents a cache in memory.
// An ExpireItem value of 0 means that items should not be expired based
// on time in memory.
type FileCache struct {
	items      map[string]*cacheItem
	in_pipe    chan string
	MaxItems   int   // Maximum number of files to cache
	MaxSize    int64 // Maximum file size to store
	ExpireItem int   // Seconds a file should be cached for
	Every      int   // Run an expiration check Every seconds
}

// CacheItem represents an item in the cache
type cacheItem struct {
	Content    []byte
	Size       int64
	Lastaccess time.Time
	Modified   time.Time
}

// NewCache returns an initialised (barely) cache.
// Note this will only do the minimum amount of initialisation: setting up
// the internal file store and such. Expiring items in the cache will not
// work properly.
func NewCache() *FileCache {
	cache := new(FileCache)
	return cache
}

// NewDefaultCache returns a new FileCache with sane defaults.
func NewDefaultCache() *FileCache {
	cache := new(FileCache)
	cache.MaxItems = DefaultMaxItems
	cache.MaxSize = DefaultMaxSize
	cache.ExpireItem = DefaultExpireItem
	cache.Every = DefaultEvery
	return cache
}

// Active returns true if the cache has been started, and false otherwise.
func (cache *FileCache) Active() bool {
	if cache.in_pipe == nil || cache.items == nil {
		return false
	}
	return true
}

// Size returns the number of entries in the cache.
func (cache *FileCache) Size() int {
	return len(cache.items)
}

// FileSize returns the sum of the file sizes stored in the cache
func (cache *FileCache) FileSize() (totalSize int64) {
	for _, itm := range cache.items {
		totalSize += itm.Size
	}
	return
}

// StoredFiles returns the list of files stored in the cache.
func (cache *FileCache) StoredFiles() (fileList []string) {
	fileList = make([]string, 0)
	for name, _ := range cache.items {
		fileList = append(fileList, name)
	}
	return
}

// FileChanged returns true if file should be expired based on mtime.
// If the file has changed on disk or no longer exists, it should be
// expired.
func (cache *FileCache) changed(name string) bool {
	itm, ok := cache.items[name]
	if !ok {
		return true
	}
	fi, err := os.Stat(name)
	if err != nil {
		return true
	} else if !itm.Modified.Equal(fi.ModTime()) {
		return true
	}
	return false
}

// Expired returns true if the item has not been accessed recently.
func (cache *FileCache) expired(name string) bool {
	itm, ok := cache.items[name]
	if !ok {
		return true
	}
	dur := time.Now().Sub(itm.Lastaccess)
	sec, err := strconv.Atoi(fmt.Sprintf("%0.0f", dur.Seconds()))
	if err != nil {
		return true
	} else if sec >= cache.ExpireItem {
		return true
	}
	return false
}

// item_expired returns true if an item is expired.
func (cache *FileCache) item_expired(name string) bool {
	if cache.changed(name) {
		return true
	} else if cache.ExpireItem != 0 && cache.expired(name) {
		return true
	}
	return false
}

// InCache returns true if the item is in the cache.
func (cache *FileCache) InCache(name string) bool {
	_, ok := cache.items[name]
	return ok
}

// WriteItem writes the cache item to the specified io.Writer.
func (cache *FileCache) WriteItem(w io.Writer, name string) (err error) {
	itm, ok := cache.items[name]
	if !ok {
		err = ItemNotInCache
		return
	}
	n, err := fmt.Fprintf(w, "%s", itm.Content)
	if err != nil {
		return
	} else if int64(n) != itm.Size {
		err = WriteIncomplete
		return
	}
	return
}

// GetItem returns the content of the item and a bool if name is present.
// GetItem should be used when you are certain an object is in the cache,
// or if you want to use the cache only.
func (cache *FileCache) GetItem(name string) (content []byte, ok bool) {
	itm, ok := cache.items[name]
	if !ok {
		return
	}
	content = itm.Content
	return
}

// GetItemString is the same as GetItem, except returning a string.
func (cache *FileCache) GetItemString(name string) (content string, ok bool) {
	itm, ok := cache.items[name]
	if !ok {
		return
	}
	content = string(itm.Content)
	return
}

// ReadFile retrieves the file named by 'name'.
// If the file is not in the cache, load the file and cache the file in the 
// background. If the file was not in the cache and the read was successful,
// the error ItemNotInCache is returned to indicate that the item was pulled
// from the filesystem and not the cache.
func (cache *FileCache) ReadFile(name string) (content []byte, err error) {
	if cache.InCache(name) {
		content, _ = cache.GetItem(name)
	} else {
		go cache.Cache(name)
		content, err = ioutil.ReadFile(name)
		if err == nil {
			err = ItemNotInCache
		}
	}
	return
}

// ReadFileString is the same as ReadFile, except returning a string.
func (cache *FileCache) ReadFileString(name string) (content string, err error) {
	if cache.InCache(name) {
		content, _ = cache.GetItemString(name)
	} else {
		go cache.Cache(name)
		raw, err := ioutil.ReadFile(name)
		if err == nil {
			err = ItemNotInCache
			content = string(raw)
		}
	}
	return
}

// WriteFile writes the file named by 'name' to the specified io.Writer.
// If the file is in the cache, it is loaded from the cache; otherwise,
// it is read from the filesystem and the file is cached in the background.
func (cache *FileCache) WriteFile(w io.Writer, name string) (err error) {
	if cache.InCache(name) {
		err = cache.WriteItem(w, name)
	} else {
		var fi os.FileInfo
		fi, err = os.Stat(name)
		if err != nil {
			return
		} else if fi.IsDir() {
			return ItemIsDirectory
		}
		go cache.Cache(name)
		var file *os.File
		file, err = os.Open(name)
		if err != nil {
			return
		}
		defer file.Close()
		_, err = io.Copy(w, file)
	}
	return
}

// add_item is an internal function for adding an item to the cache.
func (cache *FileCache) add_item(name string) (err error) {
	ok := cache.InCache(name)
	expired := cache.item_expired(name)
	if ok && !expired {
		return nil
	} else if ok {
		delete(cache.items, name)
	}

	fi, err := os.Stat(name)
	if err != nil {
		return
	} else if fi.Mode().IsDir() {
		return ItemIsDirectory
	} else if fi.Size() > cache.MaxSize {
		return ItemTooLarge
	}

	content, err := ioutil.ReadFile(name)
	if err != nil {
		return
	}

	itm := new(cacheItem)
	itm.Content = content
	itm.Size = fi.Size()
	itm.Modified = fi.ModTime()
	itm.Lastaccess = time.Now()
	cache.items[name] = itm
	if !cache.InCache(name) {
		return ItemNotInCache
	}
	return nil
}

// item_listener is a goroutine that listens for incoming files and caches
// them.
func (cache *FileCache) item_listener() {
	for {
		name, closed := <-cache.in_pipe
		if !closed {
			return
		}
		cache.add_item(name)
	}
}

// Cache will store the file named by 'name' to the cache.
// This function doesn't return anything as it passes the file onto the
// incoming pipe; the file will be cached asynchronously. Errors will
// not be returned. 
func (cache *FileCache) Cache(name string) {
	if cache.Size() == cache.MaxItems {
		cache.expire_oldest(true)
	}
	cache.in_pipe <- name
}

// CacheNow immediately caches the file named by 'name'.
func (cache *FileCache) CacheNow(name string) (err error) {
	if cache.Size() == cache.MaxItems {
		cache.expire_oldest(true)
	}
	return cache.add_item(name)
}

// Start activates the file cache; it will 
func (cache *FileCache) Start() {
	if cache.in_pipe != nil {
		close(cache.in_pipe)
	}
	cache.items = make(map[string]*cacheItem, 0)
	cache.in_pipe = make(chan string, NewCachePipeSize)
	go cache.item_listener()
	go cache.vaccuum()
}

// expire_oldest is used to expire the oldest item in the cache.
// The force argument is used to indicate it should remove at least one
// entry; for example, if a large number of files are cached at once, none
// may appear older than another.
func (cache *FileCache) expire_oldest(force bool) {
	oldest := time.Now()
	oldest_name := ""

	for name, itm := range cache.items {
		if force && oldest_name == "" {
			oldest = itm.Lastaccess
			oldest_name = name
		} else if itm.Lastaccess.Before(oldest) {
			oldest = itm.Lastaccess
			oldest_name = name
		}
	}
	if oldest_name != "" {
		delete(cache.items, oldest_name)
	}
}

// vaccuum is a background goroutine responsible for cleaning the cache.
// It runs periodically, every cache.Every seconds. If cache.Every is set
// to 0, it will not run.
func (cache *FileCache) vaccuum() {
	if cache.Every < 1 {
		return
	}

	dur, err := time.ParseDuration(fmt.Sprintf("%ds", cache.Every))
	if err != nil {
		panic(err.Error())
	}
	for {
		<-time.After(time.Duration(dur))
		if cache.items == nil {
			return
		}
		for name, _ := range cache.items {
			if cache.item_expired(name) {
				delete(cache.items, name)
			}
		}
		for size := cache.Size(); size > cache.MaxItems; size = cache.Size() {
			cache.expire_oldest(true)
		}
	}
}

// Stop turns off the file cache.
// This closes the concurrent caching mechanism, destroys the cache, and
// the background scanner that it should stop.
// If there are any items or cache operations ongoing while Stop() is called,
// it is undefined how they will behave. 
func (cache *FileCache) Stop() {
	if cache.in_pipe != nil {
		close(cache.in_pipe)
	}
	for name, _ := range cache.items {
		delete(cache.items, name)
	}
	cache.items = nil
}

// RemoveItem immediately removes the item from the cache if it is present.
// It returns a boolean indicating whether anything was removed, and an error
// if an error has occurred.
func (cache *FileCache) Remove(name string) (ok bool, err error) {
	_, ok = cache.items[name]
	if !ok {
		return
	}
	delete(cache.items, name)
	_, valid := cache.items[name]
	if valid {
		ok = false
	}
	return
}
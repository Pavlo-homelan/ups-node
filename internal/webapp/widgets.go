package webapp

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const (
	maxWidgetZipBytes     = 2 * 1024 * 1024
	maxWidgetPackageBytes = 2 * 1024 * 1024
	maxWidgetFileCount    = 40
)

var builtinWidgetStyles = []widgetStyle{
	{ID: "neon", Label: "1. Neon Tech Widget"},
	{ID: "glass", Label: "2. Glassmorphism Dashboard"},
	{ID: "oled", Label: "3. Minimal OLED Widget"},
	{ID: "home", Label: "4. Smart Home Energy Card"},
	{ID: "control", Label: "5. Futuristic Control Panel"},
	{ID: "mobile", Label: "6. Mobile App Style Widget"},
	{ID: "industrial", Label: "7. Industrial Monitoring Widget"},
	{ID: "liquid", Label: "8. Liquid Battery Widget"},
	{ID: "radial", Label: "9. Radial Energy Hub"},
	{ID: "premium", Label: "10. Premium Dark UI"},
}

var allowedWidgetSuffixes = map[string]struct{}{
	".css": {}, ".gif": {}, ".jpeg": {}, ".jpg": {}, ".json": {}, ".otf": {},
	".png": {}, ".ttf": {}, ".webp": {}, ".woff": {}, ".woff2": {}, ".xml": {},
}

type widgetStyle struct {
	ID         string `json:"id"`
	Label      string `json:"label"`
	Version    string `json:"version,omitempty"`
	Author     string `json:"author,omitempty"`
	PackageDir string `json:"package_dir,omitempty"`
	CSSFile    string `json:"css_file,omitempty"`
	Filename   string `json:"filename,omitempty"`
	CSSURL     string `json:"css_url,omitempty"`
	Custom     bool   `json:"custom"`
}

type widgetManager struct {
	storagePath  string
	manifestPath string
}

type widgetUploadError struct {
	key    string
	params map[string]string
}

func (err widgetUploadError) Error() string { return err.key }

func newWidgetManager(storagePath string) widgetManager {
	return widgetManager{
		storagePath:  storagePath,
		manifestPath: filepath.Join(storagePath, "widgets.json"),
	}
}

func (manager widgetManager) listOptions() []widgetStyle {
	options := make([]widgetStyle, 0, len(builtinWidgetStyles))
	for _, style := range builtinWidgetStyles {
		style.Custom = false
		options = append(options, style)
	}
	return append(options, manager.listCustom()...)
}

func (manager widgetManager) listCustom() []widgetStyle {
	manifest := manager.readManifest()
	ids := make([]string, 0, len(manifest))
	for id := range manifest {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(left int, right int) bool {
		return strings.ToLower(manifest[ids[left]].Label) < strings.ToLower(manifest[ids[right]].Label)
	})

	styles := make([]widgetStyle, 0, len(ids))
	for _, id := range ids {
		meta := manifest[id]
		if meta.PackageDir != "" {
			cssFile := meta.CSSFile
			if cssFile == "" {
				cssFile = "style.css"
			}
			if !regularFile(filepath.Join(manager.storagePath, meta.PackageDir, filepath.FromSlash(cssFile))) {
				continue
			}
			styles = append(styles, widgetStyle{
				ID: id, Label: firstNonEmpty(meta.Label, id), Version: meta.Version, Author: meta.Author,
				PackageDir: meta.PackageDir, CSSFile: cssFile, Custom: true,
				CSSURL: "/widgets/" + pathEscape(id) + "/" + pathEscape(cssFile),
			})
			continue
		}
		filename := meta.Filename
		if filename == "" {
			filename = id + ".css"
		}
		if !regularFile(filepath.Join(manager.storagePath, filename)) {
			continue
		}
		styles = append(styles, widgetStyle{
			ID: id, Label: firstNonEmpty(meta.Label, id), Filename: filename, Custom: true,
			CSSURL: "/widgets/" + pathEscape(filename),
		})
	}
	return styles
}

func (manager widgetManager) custom(styleID string) (widgetStyle, bool) {
	for _, style := range manager.listCustom() {
		if style.ID == styleID {
			return style, true
		}
	}
	return widgetStyle{}, false
}

func (manager widgetManager) installPackage(fileHeader *multipart.FileHeader) (widgetStyle, error) {
	if fileHeader == nil || fileHeader.Filename == "" {
		return widgetStyle{}, widgetUploadError{key: "widget.error.select_zip"}
	}
	originalName := filepath.Base(fileHeader.Filename)
	if strings.ToLower(filepath.Ext(originalName)) != ".zip" {
		return widgetStyle{}, widgetUploadError{key: "widget.error.zip_only"}
	}
	uploaded, err := fileHeader.Open()
	if err != nil {
		return widgetStyle{}, err
	}
	defer uploaded.Close()
	packageBytes, err := io.ReadAll(io.LimitReader(uploaded, maxWidgetZipBytes+1))
	if err != nil {
		return widgetStyle{}, err
	}
	if len(packageBytes) > maxWidgetZipBytes {
		return widgetStyle{}, widgetUploadError{key: "widget.error.zip_too_large"}
	}
	files, err := readZipFiles(packageBytes)
	if err != nil {
		return widgetStyle{}, err
	}
	meta, err := readPackageMeta(files, originalName)
	if err != nil {
		return widgetStyle{}, err
	}
	if err := validateCSS(files[meta.CSSFile]); err != nil {
		return widgetStyle{}, err
	}

	styleID := styleIDFrom(firstNonEmpty(meta.ID, meta.Label))
	packageDir := styleID
	if err := os.MkdirAll(manager.storagePath, 0o770); err != nil {
		return widgetStyle{}, err
	}
	manifest := manager.readManifest()
	if previous, ok := manifest[styleID]; ok {
		_ = manager.removeMetaFiles(previous)
	}
	packagePath := filepath.Join(manager.storagePath, packageDir)
	if err := os.RemoveAll(packagePath); err != nil {
		return widgetStyle{}, err
	}
	if err := os.MkdirAll(packagePath, 0o770); err != nil {
		return widgetStyle{}, err
	}
	for relativePath, contents := range files {
		target := filepath.Join(packagePath, filepath.FromSlash(relativePath))
		if err := os.MkdirAll(filepath.Dir(target), 0o770); err != nil {
			return widgetStyle{}, err
		}
		if err := os.WriteFile(target, contents, 0o660); err != nil {
			return widgetStyle{}, err
		}
	}
	manifest[styleID] = widgetStyle{
		Label: meta.Label, Version: meta.Version, Author: meta.Author, PackageDir: packageDir, CSSFile: meta.CSSFile,
	}
	if err := manager.writeManifest(manifest); err != nil {
		return widgetStyle{}, err
	}
	style, ok := manager.custom(styleID)
	if !ok {
		return widgetStyle{}, fmt.Errorf("installed widget %s is unavailable", styleID)
	}
	return style, nil
}

func (manager widgetManager) deleteCustom(styleID string) error {
	manifest := manager.readManifest()
	meta, ok := manifest[styleID]
	if !ok {
		return widgetUploadError{key: "widget.error.custom_not_found"}
	}
	if err := manager.removeMetaFiles(meta); err != nil {
		return err
	}
	delete(manifest, styleID)
	return manager.writeManifest(manifest)
}

func (manager widgetManager) cssPath(filename string) (string, bool) {
	if !safeName(filename) || !strings.HasSuffix(filename, ".css") {
		return "", false
	}
	cssPath := filepath.Join(manager.storagePath, filename)
	return cssPath, regularFile(cssPath)
}

func (manager widgetManager) filePath(styleID string, filename string) (string, bool) {
	style, ok := manager.custom(styleID)
	if !ok || style.PackageDir == "" {
		return "", false
	}
	relativePath, ok := safeRelativePath(filename)
	if !ok {
		return "", false
	}
	if _, allowed := allowedWidgetSuffixes[strings.ToLower(filepath.Ext(relativePath))]; !allowed {
		return "", false
	}
	packagePath, err := filepath.Abs(filepath.Join(manager.storagePath, style.PackageDir))
	if err != nil {
		return "", false
	}
	assetPath, err := filepath.Abs(filepath.Join(packagePath, relativePath))
	if err != nil || !strings.HasPrefix(assetPath, packagePath+string(os.PathSeparator)) {
		return "", false
	}
	return assetPath, regularFile(assetPath)
}

func widgetMimeType(filename string) string {
	switch strings.ToLower(filepath.Ext(filename)) {
	case ".otf":
		return "font/otf"
	case ".ttf":
		return "font/ttf"
	case ".woff":
		return "font/woff"
	case ".woff2":
		return "font/woff2"
	default:
		if value := mime.TypeByExtension(filepath.Ext(filename)); value != "" {
			return value
		}
		return "application/octet-stream"
	}
}

func (manager widgetManager) readManifest() map[string]widgetStyle {
	contents, err := os.ReadFile(manager.manifestPath)
	if err != nil {
		return map[string]widgetStyle{}
	}
	var manifest map[string]widgetStyle
	if err := json.Unmarshal(contents, &manifest); err != nil {
		return map[string]widgetStyle{}
	}
	if manifest == nil {
		return map[string]widgetStyle{}
	}
	return manifest
}

func (manager widgetManager) writeManifest(manifest map[string]widgetStyle) error {
	if err := os.MkdirAll(manager.storagePath, 0o770); err != nil {
		return err
	}
	contents, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	contents = append(contents, '\n')
	return os.WriteFile(manager.manifestPath, contents, 0o660)
}

func (manager widgetManager) removeMetaFiles(meta widgetStyle) error {
	if meta.PackageDir != "" {
		name := cleanName(meta.PackageDir)
		if name == "" {
			return fmt.Errorf("refusing unsafe widget package path %q", meta.PackageDir)
		}
		return os.RemoveAll(filepath.Join(manager.storagePath, name))
	}
	if meta.Filename != "" {
		name := cleanName(meta.Filename)
		if name == "" {
			return fmt.Errorf("refusing unsafe widget file path %q", meta.Filename)
		}
		filePath := filepath.Join(manager.storagePath, name)
		if regularFile(filePath) {
			return os.Remove(filePath)
		}
	}
	return nil
}

type packageMeta struct {
	ID      string
	Label   string
	Version string
	Author  string
	CSSFile string
}

func readZipFiles(packageBytes []byte) (map[string][]byte, error) {
	reader, err := zip.NewReader(bytes.NewReader(packageBytes), int64(len(packageBytes)))
	if err != nil {
		return nil, widgetUploadError{key: "widget.error.bad_zip"}
	}
	entries := make([]*zip.File, 0, len(reader.File))
	for _, file := range reader.File {
		if file.FileInfo().IsDir() {
			continue
		}
		relativePath, ok := safeZipPath(file.Name)
		if !ok {
			return nil, widgetUploadError{key: "widget.error.bad_zip_path", params: map[string]string{"path": file.Name}}
		}
		if relativePath == "" {
			continue
		}
		entries = append(entries, file)
	}
	if len(entries) == 0 {
		return nil, widgetUploadError{key: "widget.error.empty_zip"}
	}
	if len(entries) > maxWidgetFileCount {
		return nil, widgetUploadError{key: "widget.error.too_many_files"}
	}
	root := singleRootFolder(entries)
	files := map[string][]byte{}
	totalSize := uint64(0)
	actualSize := 0
	for _, file := range entries {
		relativePath, _ := safeZipPath(file.Name)
		if root != "" {
			relativePath = strings.TrimPrefix(relativePath, root+"/")
		}
		if relativePath == "" {
			continue
		}
		if _, allowed := allowedWidgetSuffixes[strings.ToLower(path.Ext(relativePath))]; !allowed {
			return nil, widgetUploadError{key: "widget.error.bad_file_type", params: map[string]string{"path": relativePath}}
		}
		totalSize += file.UncompressedSize64
		if totalSize > maxWidgetPackageBytes {
			return nil, widgetUploadError{key: "widget.error.package_too_large"}
		}
		if _, exists := files[relativePath]; exists {
			return nil, widgetUploadError{key: "widget.error.duplicate_file", params: map[string]string{"path": relativePath}}
		}
		opened, err := file.Open()
		if err != nil {
			return nil, err
		}
		contents, err := io.ReadAll(io.LimitReader(opened, int64(maxWidgetPackageBytes)+1))
		closeErr := opened.Close()
		if err != nil {
			return nil, err
		}
		if closeErr != nil {
			return nil, closeErr
		}
		actualSize += len(contents)
		if actualSize > maxWidgetPackageBytes {
			return nil, widgetUploadError{key: "widget.error.package_too_large"}
		}
		files[relativePath] = contents
	}
	if len(files) == 0 {
		return nil, widgetUploadError{key: "widget.error.empty_zip"}
	}
	return files, nil
}

func readPackageMeta(files map[string][]byte, originalName string) (packageMeta, error) {
	meta, err := readWidgetJSON(files)
	if err != nil {
		return packageMeta{}, err
	}
	if meta == nil {
		meta, err = readAddonXML(files)
		if err != nil {
			return packageMeta{}, err
		}
	}
	cssFile := firstNonEmpty(meta["stylesheet"], meta["css"], "style.css")
	if cssFile == "" {
		cssFile = "style.css"
	}
	cssFile, ok := safeRelativePathSlash(cssFile)
	if !ok || files[cssFile] == nil {
		candidates := make([]string, 0, 1)
		for name := range files {
			if strings.HasSuffix(name, ".css") {
				candidates = append(candidates, name)
			}
		}
		if len(candidates) != 1 {
			return packageMeta{}, widgetUploadError{key: "widget.error.missing_css"}
		}
		cssFile = candidates[0]
	}
	label := strings.TrimSpace(firstNonEmpty(meta["name"], meta["label"], strings.TrimSuffix(filepath.Base(originalName), filepath.Ext(originalName)), "Custom widget"))
	return packageMeta{
		ID: meta["id"], Label: label, Version: meta["version"],
		Author: firstNonEmpty(meta["author"], meta["provider"]), CSSFile: cssFile,
	}, nil
}

func readWidgetJSON(files map[string][]byte) (map[string]string, error) {
	contents, ok := files["widget.json"]
	if !ok {
		return nil, nil
	}
	var raw map[string]any
	if err := json.Unmarshal(contents, &raw); err != nil {
		return nil, widgetUploadError{key: "widget.error.bad_widget_json"}
	}
	meta := make(map[string]string, len(raw))
	for key, value := range raw {
		if text, ok := value.(string); ok {
			meta[key] = text
		}
	}
	return meta, nil
}

func readAddonXML(files map[string][]byte) (map[string]string, error) {
	contents, ok := files["addon.xml"]
	if !ok {
		return map[string]string{}, nil
	}
	var addon struct {
		ID         string `xml:"id,attr"`
		Name       string `xml:"name,attr"`
		Version    string `xml:"version,attr"`
		Provider   string `xml:"provider-name,attr"`
		Extensions []struct {
			Stylesheet string `xml:"stylesheet,attr"`
			CSS        string `xml:"css,attr"`
		} `xml:"extension"`
	}
	if err := xml.Unmarshal(contents, &addon); err != nil {
		return nil, widgetUploadError{key: "widget.error.bad_addon_xml"}
	}
	meta := map[string]string{"id": addon.ID, "name": addon.Name, "version": addon.Version, "provider": addon.Provider}
	for _, extension := range addon.Extensions {
		if extension.Stylesheet != "" {
			meta["stylesheet"] = extension.Stylesheet
			break
		}
		if extension.CSS != "" {
			meta["css"] = extension.CSS
			break
		}
	}
	return meta, nil
}

func validateCSS(contents []byte) error {
	text := strings.ToLower(string(contents))
	for _, token := range []string{"<script", "</style", "@import", "javascript:", "data:", "http://", "https://", "url(//"} {
		if strings.Contains(text, token) {
			return widgetUploadError{key: "widget.error.blocked_css"}
		}
	}
	return nil
}

func safeZipPath(filename string) (string, bool) {
	relativePath, ok := safeRelativePathSlash(filename)
	if !ok {
		return "", false
	}
	parts := strings.Split(relativePath, "/")
	for _, part := range parts {
		if part == "__MACOSX" {
			return "", true
		}
		if strings.EqualFold(part, ".ds_store") {
			return "", true
		}
	}
	return relativePath, true
}

func safeRelativePath(pathValue string) (string, bool) {
	value, ok := safeRelativePathSlash(pathValue)
	if !ok {
		return "", false
	}
	return filepath.FromSlash(value), true
}

func safeRelativePathSlash(pathValue string) (string, bool) {
	normalized := strings.ReplaceAll(pathValue, "\\", "/")
	normalized = path.Clean(normalized)
	if normalized == "." || strings.HasPrefix(normalized, "/") || strings.HasPrefix(normalized, "../") || normalized == ".." {
		return "", false
	}
	parts := strings.Split(normalized, "/")
	for _, part := range parts {
		if !safeName(part) {
			return "", false
		}
	}
	return strings.Join(parts, "/"), true
}

func singleRootFolder(entries []*zip.File) string {
	var root string
	for _, file := range entries {
		relativePath, ok := safeZipPath(file.Name)
		if !ok || relativePath == "" {
			continue
		}
		parts := strings.Split(relativePath, "/")
		if len(parts) < 2 {
			return ""
		}
		if root == "" {
			root = parts[0]
			continue
		}
		if root != parts[0] {
			return ""
		}
	}
	return root
}

var unsafeNamePattern = regexp.MustCompile(`[^A-Za-z0-9_.-]`)
var unsafeIDPattern = regexp.MustCompile(`[^a-z0-9]+`)

func safeName(value string) bool {
	return value != "" && value != "." && value != ".." && !unsafeNamePattern.MatchString(value)
}

func cleanName(value string) string {
	if safeName(value) {
		return value
	}
	return ""
}

func styleIDFrom(value string) string {
	value = unsafeIDPattern.ReplaceAllString(strings.ToLower(value), "-")
	value = strings.Trim(value, "-")
	if value == "" {
		value = "custom-widget"
	}
	if !strings.HasPrefix(value, "user-") {
		value = "user-" + value
	}
	for _, style := range builtinWidgetStyles {
		if value == style.ID {
			return "user-" + value
		}
	}
	return value
}

func pathEscape(value string) string {
	return strings.ReplaceAll(path.Clean(value), " ", "%20")
}

func regularFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

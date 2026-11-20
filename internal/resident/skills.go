package resident

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/gilvan-Borges/HiveMind/internal/skillx"
	"github.com/gilvan-Borges/HiveMind/internal/strx"
)

const skillReviewsFile = "skill-reviews.json"

type skillReviewFile struct {
	Reviews map[string]string `json:"reviews"`
}

// HashSkillBody is the SHA-256 the IDE stores when a person reviews a skill.
func HashSkillBody(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

func skillReviewPath(baseDir string) string {
	return filepath.Join(baseDir, skillReviewsFile)
}

// LoadSkillReviews reads the host-only review hashes. Missing file is empty.
func LoadSkillReviews(baseDir string) (map[string]string, error) {
	data, err := os.ReadFile(skillReviewPath(baseDir))
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]string{}, nil
		}
		return nil, err
	}
	var f skillReviewFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, err
	}
	if f.Reviews == nil {
		f.Reviews = map[string]string{}
	}
	return f.Reviews, nil
}

// ReviewSkill stores the current hash of a native skill. A later edit drops it
// out of the prompt until the person reviews again. Project skills are refused.
func ReviewSkill(baseDir, nativeDir, name string) (string, error) {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" || strings.Contains(name, "/") || strings.Contains(name, "..") {
		return "", fmt.Errorf("nome de skill inválido")
	}
	body, err := os.ReadFile(filepath.Join(nativeDir, name, "SKILL.md"))
	if err != nil {
		return "", fmt.Errorf("skill nativa %q não encontrada", name)
	}
	sum := HashSkillBody(body)
	reviews, err := LoadSkillReviews(baseDir)
	if err != nil {
		return "", err
	}
	reviews[name] = sum
	if err := writeSkillReviews(baseDir, reviews); err != nil {
		return "", err
	}
	return sum, nil
}

func writeSkillReviews(baseDir string, reviews map[string]string) error {
	if err := os.MkdirAll(baseDir, 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(skillReviewFile{Reviews: reviews}, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	path := skillReviewPath(baseDir)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// SkillBlock is the resident skill prompt: one line plus the body, and only
// for native skills whose saved hash still matches. Disabled, unreviewed,
// mismatched and project skills stay out.
func SkillBlock(nativeDir, baseDir string) string {
	nativeDir = strings.TrimSpace(nativeDir)
	if nativeDir == "" {
		return ""
	}
	reviews, err := LoadSkillReviews(baseDir)
	if err != nil {
		reviews = map[string]string{}
	}
	var b strings.Builder
	total := 0
	for _, sk := range skillx.LoadNative(nativeDir) {
		if !sk.Enabled {
			continue
		}
		bodyPath := filepath.Join(nativeDir, filepath.FromSlash(sk.Path))
		raw, err := os.ReadFile(bodyPath)
		if err != nil {
			continue
		}
		sum := HashSkillBody(raw)
		if reviews[sk.Name] == "" || reviews[sk.Name] != sum {
			continue
		}
		line := fmt.Sprintf("- %s — %s\n", sk.Name, sk.Description)
		body := strx.TruncateBytes(strings.TrimSpace(string(raw)), skillx.DefaultBodyBytes, "\n…")
		need := len(line) + len(body)
		if total+need > skillx.DefaultTotalBytes {
			break
		}
		b.WriteString(line)
		b.WriteString(WrapUntrustedData("skill", sk.Name, body))
		b.WriteByte('\n')
		total += need
	}
	if b.Len() == 0 {
		return ""
	}
	return "[Skills nativas revisadas]\n" + b.String()
}

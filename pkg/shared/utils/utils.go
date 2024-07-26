package utils

import (
	"artemis/pkg/shared/logger"
	"artemis/pkg/shared/models"
	"regexp"
	"strings"
	"time"
)

func Slugify(s string) string {
	s = strings.ToLower(s)
	reg, err := regexp.Compile("[^a-z0-9]+")
	if err != nil {
		panic(err)
	}
	s = reg.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	return s
}

func LogDecorator[R any](f func(models.Step, *map[string]interface{}) (R, error)) func(models.Step, *map[string]interface{}) (R, error) {
	return func(step models.Step, config *map[string]interface{}) (R, error) {
		startTime := time.Now()
		resp, err := f(step, config)
		if err != nil {
			return resp, err
		}
		logger.Logger.Info("result:", "name", step.Name, "time", time.Since(startTime))
		return resp, err
	}
}

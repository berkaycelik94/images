package awsimages

import (
	"errors"
	"net/http"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/awslabs/aws-sdk-go/aws"
	"github.com/awslabs/aws-sdk-go/aws/credentials"
	"github.com/awslabs/aws-sdk-go/service/ec2"
	"github.com/hashicorp/go-multierror"
)

type AwsConfig struct {
	Region        string `toml:"region" json:"region"`
	RegionExclude string `toml:"region_exclude" json:"region_exclude"`
	AccessKey     string `toml:"access_key" json:"access_key"`
	SecretKey     string `toml:"secret_key" json:"secret_key"`
}

// AwsImages is responsible of managing AWS images (AMI's)
type AwsImages struct {
	services *multiRegion
	images   MultiImages
}

func New(conf *AwsConfig) (*AwsImages, error) {
	checkCfg := "Please check your configuration"

	if conf.Region == "" {
		return nil, errors.New("AWS Region is not set. " + checkCfg)
	}

	if conf.AccessKey == "" {
		return nil, errors.New("AWS Access Key is not set. " + checkCfg)
	}

	if conf.SecretKey == "" {
		return nil, errors.New("AWS Secret Key is not set. " + checkCfg)
	}

	// increase the timeout
	timeout := time.Second * 30
	client := &http.Client{
		Transport: &http.Transport{TLSHandshakeTimeout: timeout},
		Timeout:   timeout,
	}

	creds := credentials.NewStaticCredentials(conf.AccessKey, conf.SecretKey, "")
	awsCfg := &aws.Config{
		Credentials: creds,
		HTTPClient:  client,
		Logger:      os.Stdout,
	}

	m := newMultiRegion(awsCfg, parseRegions(conf.Region, conf.RegionExclude))
	return &AwsImages{
		services: m,
		images:   make(map[string][]*ec2.Image),
	}, nil
}

func (a *AwsImages) MultiImages(input *ec2.DescribeImagesInput) (MultiImages, error) {
	var (
		wg sync.WaitGroup
		mu sync.Mutex

		multiErrors error
	)

	images := make(map[string][]*ec2.Image)

	for r, s := range a.services.regions {
		wg.Add(1)
		go func(region string, svc *ec2.EC2) {
			resp, err := svc.DescribeImages(input)
			mu.Lock()

			if err != nil {
				multiErrors = multierror.Append(multiErrors, err)
			} else {
				// sort from oldest to newest
				if len(resp.Images) > 1 {
					sort.Sort(byTime(resp.Images))
				}

				images[region] = resp.Images
			}

			mu.Unlock()
			wg.Done()
		}(r, s)
	}

	wg.Wait()

	return images, multiErrors
}

func (a *AwsImages) ownerImages() (MultiImages, error) {
	input := &ec2.DescribeImagesInput{
		Owners: stringSlice("self"),
	}

	return a.MultiImages(input)
}

// Help prints the help message for the given command
func (a *AwsImages) Help(command string) string {
	var help string

	global := `
  -access-key      "..."       AWS Access Key (env: IMAGES_AWS_ACCESS_KEY)
  -secret-key      "..."       AWS Secret Key (env: IMAGES_AWS_SECRET_KEY)
  -region          "..."       AWS Region (env: IMAGES_AWS_REGION)
  -region-exclude  "..."       AWS Region to be excluded (env: IMAGES_AWS_REGION_EXCLUDE)
`
	switch command {
	case "modify":
		help = newModifyFlags().helpMsg
	case "delete":
		help = newDeleteFlags().helpMsg
	case "list":
		help = `Usage: images list --provider aws [options]

 List AMI properties.

Options:
	`
	case "copy":
		help = newCopyFlags().helpMsg
	default:
		return "no help found for command " + command
	}

	help += global
	return help
}

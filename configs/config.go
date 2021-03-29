package configs

// Service :nodoc:
var Service *service

// Constant :nodoc:
var Constant *constant

// S3 :nodoc:
var S3 *s3

func init() {
	Service = initService()
	Constant = initConstant()
	S3 = initS3()
}

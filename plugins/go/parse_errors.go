package goplug

func notifyParseError(callback func(error), err error) {
	if err != nil && callback != nil {
		callback(err)
	}
}

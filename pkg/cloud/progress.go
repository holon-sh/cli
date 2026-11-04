package cloud

import (
	"os"

	"golang.org/x/term"
)

func terminalFile(stream any) (*os.File, bool) {
	file, ok := stream.(*os.File)
	return file, ok && term.IsTerminal(int(file.Fd()))
}

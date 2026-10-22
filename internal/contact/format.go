package contact

import "fmt"

// MessageSubject and MessageBody format a contact message for email.
func MessageSubject(siteTitle string, in Input) string {
	s := in.Subject
	if s == "" {
		s = "New message from " + in.Name
	}
	return "[" + siteTitle + "] " + s
}

func MessageBody(in Input, adminURL string) string {
	return fmt.Sprintf("From: %s <%s>\nSubject: %s\n\n%s\n\n—\nSent through the contact form. Reply to this email to answer %s.\n%s\n",
		in.Name, in.Email, in.Subject, in.Body, in.Name, adminURL)
}

package market

import "testing"

func TestWriteOrderValidateRequiresOneBasedComponents(t *testing.T) {
	tests := []struct {
		name    string
		order   WriteOrder
		wantErr bool
	}{
		{name: "first valid order", order: WriteOrder{SwitchVersion: 1, RunSequence: 1, PageSequence: 1}},
		{name: "later valid order", order: WriteOrder{SwitchVersion: 2, RunSequence: 3, PageSequence: 4}},
		{name: "zero switch version", order: WriteOrder{RunSequence: 1, PageSequence: 1}, wantErr: true},
		{name: "negative switch version", order: WriteOrder{SwitchVersion: -1, RunSequence: 1, PageSequence: 1}, wantErr: true},
		{name: "zero run sequence", order: WriteOrder{SwitchVersion: 1, PageSequence: 1}, wantErr: true},
		{name: "negative run sequence", order: WriteOrder{SwitchVersion: 1, RunSequence: -1, PageSequence: 1}, wantErr: true},
		{name: "zero page sequence", order: WriteOrder{SwitchVersion: 1, RunSequence: 1}, wantErr: true},
		{name: "negative page sequence", order: WriteOrder{SwitchVersion: 1, RunSequence: 1, PageSequence: -1}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.order.Validate(); (err != nil) != tt.wantErr {
				t.Fatalf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestWriteOrderCompareUsesLexicographicOrder(t *testing.T) {
	tests := []struct {
		name  string
		left  WriteOrder
		right WriteOrder
		want  int
	}{
		{
			name:  "equal",
			left:  WriteOrder{SwitchVersion: 2, RunSequence: 3, PageSequence: 4},
			right: WriteOrder{SwitchVersion: 2, RunSequence: 3, PageSequence: 4},
		},
		{
			name:  "lower switch wins over later run and page",
			left:  WriteOrder{SwitchVersion: 1, RunSequence: 99, PageSequence: 99},
			right: WriteOrder{SwitchVersion: 2, RunSequence: 1, PageSequence: 1},
			want:  -1,
		},
		{
			name:  "higher switch wins over earlier run and page",
			left:  WriteOrder{SwitchVersion: 2, RunSequence: 1, PageSequence: 1},
			right: WriteOrder{SwitchVersion: 1, RunSequence: 99, PageSequence: 99},
			want:  1,
		},
		{
			name:  "lower run wins over later page",
			left:  WriteOrder{SwitchVersion: 2, RunSequence: 3, PageSequence: 99},
			right: WriteOrder{SwitchVersion: 2, RunSequence: 4, PageSequence: 1},
			want:  -1,
		},
		{
			name:  "higher run wins over earlier page",
			left:  WriteOrder{SwitchVersion: 2, RunSequence: 4, PageSequence: 1},
			right: WriteOrder{SwitchVersion: 2, RunSequence: 3, PageSequence: 99},
			want:  1,
		},
		{
			name:  "lower page",
			left:  WriteOrder{SwitchVersion: 2, RunSequence: 3, PageSequence: 4},
			right: WriteOrder{SwitchVersion: 2, RunSequence: 3, PageSequence: 5},
			want:  -1,
		},
		{
			name:  "higher page",
			left:  WriteOrder{SwitchVersion: 2, RunSequence: 3, PageSequence: 5},
			right: WriteOrder{SwitchVersion: 2, RunSequence: 3, PageSequence: 4},
			want:  1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.left.Validate(); err != nil {
				t.Fatalf("left Validate() error = %v", err)
			}
			if err := tt.right.Validate(); err != nil {
				t.Fatalf("right Validate() error = %v", err)
			}
			if got := tt.left.Compare(tt.right); got != tt.want {
				t.Fatalf("Compare() = %d, want %d", got, tt.want)
			}
		})
	}
}

package homekit

import "github.com/brutella/hap/service"

func mapSwitch(b *accessoryBuilder) error {
	primary := true
	for _, item := range b.exposure.Entities {
		if item.Role != roleOn {
			continue
		}
		if b.exposure.Kind == KindOutlet {
			s := service.NewOutlet()
			bindSet(b, s.On.C, item.EntityID, readBool, boolWriter(item.EntityID, ""))
			b.bind(s.OutletInUse.C, item.EntityID, readBool, nil)
			if err := b.addService(slot(item), s.S, primary); err != nil {
				return err
			}
		} else {
			s := service.NewSwitch()
			bindSet(b, s.On.C, item.EntityID, readBool, boolWriter(item.EntityID, ""))
			if err := b.addService(slot(item), s.S, primary); err != nil {
				return err
			}
		}
		primary = false
	}
	return nil
}

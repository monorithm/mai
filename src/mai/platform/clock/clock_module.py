from bustan import Module

from .clock import Clock


@Module(providers=[Clock], exports=[Clock])
class ClockModule:
    pass
